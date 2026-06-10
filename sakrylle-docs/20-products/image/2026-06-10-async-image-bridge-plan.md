# Async Image Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route slow gpt-image-2 renders through 12ai's async task API (submit→poll→fetch) behind the existing synchronous OpenAI images endpoints, eliminating the Cloudflare 100s/524 timeout, and bill via a synthesized usage object fed to the existing token-billing path.

**Architecture:** A dedicated, unit-testable module branches off `forwardOpenAIImagesAPIKey` when the bound account is async-flagged. The branch translates the OpenAI request to a 12ai task submit, polls until done, downloads the image(s), writes a `b64_json` response to the client, and returns an `OpenAIForwardResult` whose `Usage` is synthesized from `(size, quality, reference images)`. The existing `RecordUsage` then bills it as tokens because the channel is `billing_mode=token`. Client protocol is unchanged.

**Tech Stack:** Go (gateway, package `service`), gin, `net/http`, `net/http/httptest` for tests. Reference spec: `sakrylle-docs/20-products/image/2026-06-10-async-image-bridge-design.md`.

---

## Plumbing decisions (locked for this plan)

- **Branch point:** at the top of `forwardOpenAIImagesAPIKey` (`backend/internal/service/openai_images.go:559`), immediately after `upstreamModel` is computed/validated (~line 578), before building the sync upstream request.
- **Config location refinement (deviation from spec §5③):** the size×quality token table lives in **`account.Credentials["async_image_synth"]`** (the account is in scope at the branch; the channel's `features_config` is not threaded into `ForwardImages`). Account 1131 is dedicated to the async upstream, so account-level config is appropriate. Rates (¥8/¥48) still live in `channel_model_pricing` (reused token billing); margin still on group 21 `rate_multiplier`.
- **Error contract:** async failures write the client error via `writeOpenAIImagesUpstreamErrorResponse(c, e)` then return `*OpenAIImagesUpstreamError`. The handler's `errors.As(err,&imageUpstreamErr)` branch then logs and returns **without** failover/retry/billing (verified in `handler/openai_images.go`). Client disconnect returns `ctx.Err()` (no response written, no billing).
- **Success contract:** async writes the `b64_json` body with `c.Data(200,"application/json",body)` (mirrors `handleOpenAIImagesNonStreamingResponse`), then returns a result with `err==nil` → handler success path runs `RecordUsage`.

## File structure

- Create `backend/internal/service/openai_images_async.go` — `AsyncImageClient` (Submit/Poll/FetchAsB64), `ForwardImagesAsync`, account async helpers, error/response helpers.
- Create `backend/internal/service/openai_images_usage_synth.go` — `AsyncSynthConfig`, `ParseAsyncSynthConfig`, `SynthesizeAsyncImageUsage`, small pure helpers.
- Create `backend/internal/service/openai_images_usage_synth_test.go`
- Create `backend/internal/service/openai_images_async_test.go`
- Modify `backend/internal/service/openai_images.go` — add the branch (~line 578).

Verified existing types used (do not redefine):
`OpenAIUsage{InputTokens,OutputTokens,CacheCreationInputTokens,CacheReadInputTokens,ImageOutputTokens int}`;
`OpenAIForwardResult{Usage,Model,UpstreamModel,Duration,ImageCount,ImageSize,ImageInputSize,...}`;
`OpenAIImagesRequest{Size,SizeTier,Quality,N,Prompt,Multipart,Uploads []OpenAIImagesUpload,MaskUpload *OpenAIImagesUpload,InputImageURLs []string,MaskImageURL string,...}`;
`OpenAIImagesUpload{FieldName,FileName,ContentType string,Data []byte,Width,Height int}`;
`OpenAIImagesUpstreamError{StatusCode int,ErrorType,Code,Message,Param,UpstreamRequestID string}`;
`func NormalizeImageBillingTierOrDefault(size string) string` → "1K"/"2K"/"4K";
`func (a *Account) GetCredential(key string) string`, `GetOpenAIApiKey() string`;
`func writeOpenAIImagesUpstreamErrorResponse(c *gin.Context, err *OpenAIImagesUpstreamError) bool`;
`func sanitizeUpstreamErrorMessage(string) string`.

---

## Task 1: Account async config helpers

**Files:**
- Create: `backend/internal/service/openai_images_async.go`
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing test**

```go
package service

import "testing"

func TestAccountAsyncConfig(t *testing.T) {
	a := &Account{Credentials: map[string]any{
		"async_enabled":    "true",
		"async_base_url":   "https://cdn.12ai.org/",
		"api_key":          "sk-x",
		"poll_interval_ms": float64(2000),
	}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	if !a.IsAsyncImage() {
		t.Fatal("expected IsAsyncImage true")
	}
	if got := a.AsyncImageBaseURL(); got != "https://cdn.12ai.org" {
		t.Fatalf("base url = %q", got)
	}
	if got := a.AsyncPollIntervalMs(); got != 2000 {
		t.Fatalf("poll interval = %d", got)
	}
	if got := a.AsyncMaxWaitMs(); got != 240000 {
		t.Fatalf("default max wait = %d", got)
	}
	off := &Account{Credentials: map[string]any{}}
	if off.IsAsyncImage() {
		t.Fatal("expected IsAsyncImage false when unset")
	}
}
```

> Note: confirm the exact OpenAI platform constant name (`PlatformOpenAI`) via `grep -rn "PlatformOpenAI\|IsOpenAI" backend/internal/service/account.go`; adjust the literal if different. `IsAsyncImage` itself does not depend on platform.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestAccountAsyncConfig -v`
Expected: FAIL — `a.IsAsyncImage undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/service/openai_images_async.go`:

```go
package service

import (
	"strconv"
	"strings"
)

// IsAsyncImage reports whether this account routes image requests through the
// async task bridge (12ai cdn task API) instead of the synchronous endpoint.
func (a *Account) IsAsyncImage() bool {
	return strings.EqualFold(strings.TrimSpace(a.GetCredential("async_enabled")), "true")
}

// AsyncImageBaseURL is the async task host, e.g. https://cdn.12ai.org (no trailing slash).
func (a *Account) AsyncImageBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(a.GetCredential("async_base_url")), "/")
}

func (a *Account) asyncIntCredential(key string, def int) int {
	v := strings.TrimSpace(a.GetCredential(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// AsyncPollIntervalMs is the poll interval in ms (default 3000).
func (a *Account) AsyncPollIntervalMs() int { return a.asyncIntCredential("poll_interval_ms", 3000) }

// AsyncMaxWaitMs is the max total poll wait in ms (default 240000).
func (a *Account) AsyncMaxWaitMs() int { return a.asyncIntCredential("max_wait_ms", 240000) }
```

> `GetCredential` already stringifies `float64`/`json.Number`, so numeric `poll_interval_ms` parses correctly.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestAccountAsyncConfig -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): account async config helpers"
```

---

## Task 2: Synth config parsing + usage synthesizer (billing core)

**Files:**
- Create: `backend/internal/service/openai_images_usage_synth.go`
- Test: `backend/internal/service/openai_images_usage_synth_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package service

import "testing"

func sampleSynthConfig() AsyncSynthConfig {
	return AsyncSynthConfig{
		OutputTokenTable: map[string]map[string]int{
			"1K": {"low": 196, "medium": 1756, "high": 7023},
			"2K": {"low": 400, "medium": 3500, "high": 14000},
		},
		RefImageTokens: map[string]int{"1K": 1024, "2K": 4096},
	}
}

func TestSynthesizeGeneration(t *testing.T) {
	u, ok := SynthesizeAsyncImageUsage(AsyncSynthInput{
		Size: "1024x1024", Quality: "high", N: 1, Prompt: "a cat",
	}, sampleSynthConfig())
	if !ok {
		t.Fatal("expected exact (non-fallback) match")
	}
	if u.OutputTokens != 7023 {
		t.Fatalf("output tokens = %d, want 7023", u.OutputTokens)
	}
	if u.InputTokens == 0 {
		t.Fatalf("expected small text input tokens, got 0")
	}
}

func TestSynthesizeAutoBecomesMedium(t *testing.T) {
	u, _ := SynthesizeAsyncImageUsage(AsyncSynthInput{Size: "1024x1024", Quality: "auto", N: 1}, sampleSynthConfig())
	if u.OutputTokens != 1756 {
		t.Fatalf("auto should bill medium=1756, got %d", u.OutputTokens)
	}
}

func TestSynthesizeMultiImage(t *testing.T) {
	u, _ := SynthesizeAsyncImageUsage(AsyncSynthInput{Size: "1024x1024", Quality: "low", N: 3}, sampleSynthConfig())
	if u.OutputTokens != 196*3 {
		t.Fatalf("n=3 low = %d, want %d", u.OutputTokens, 196*3)
	}
}

func TestSynthesizeEditRefImageInput(t *testing.T) {
	u, _ := SynthesizeAsyncImageUsage(AsyncSynthInput{
		Size: "1024x1024", Quality: "medium", N: 1,
		RefImages: []OpenAIImagesUpload{{Width: 1024, Height: 1024}},
	}, sampleSynthConfig())
	// ref 1024 image tokens * 1.6 = 1638, plus tiny text input
	if u.InputTokens < 1638 {
		t.Fatalf("edit input tokens = %d, want >= 1638 (1024*1.6)", u.InputTokens)
	}
	if u.OutputTokens != 1756 {
		t.Fatalf("edit output = %d, want 1756", u.OutputTokens)
	}
}

func TestSynthesizeMissingCellFallsBackToRowMax(t *testing.T) {
	cfg := AsyncSynthConfig{OutputTokenTable: map[string]map[string]int{"1K": {"low": 196, "medium": 1756}}}
	u, ok := SynthesizeAsyncImageUsage(AsyncSynthInput{Size: "1024x1024", Quality: "high", N: 1}, cfg)
	if ok {
		t.Fatal("expected fallback (ok=false) for missing high cell")
	}
	if u.OutputTokens != 1756 {
		t.Fatalf("fallback should use row max 1756, got %d", u.OutputTokens)
	}
}

func TestParseAsyncSynthConfig(t *testing.T) {
	features := map[string]any{
		"async_image_synth": map[string]any{
			"output_token_table": map[string]any{
				"1K": map[string]any{"low": float64(196), "high": float64(7023)},
			},
			"ref_image_tokens": map[string]any{"1K": float64(1024)},
		},
	}
	cfg, ok := ParseAsyncSynthConfig(features)
	if !ok || cfg.OutputTokenTable["1K"]["high"] != 7023 || cfg.RefImageTokens["1K"] != 1024 {
		t.Fatalf("parse failed: %+v ok=%v", cfg, ok)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/service/ -run 'TestSynthesize|TestParseAsyncSynthConfig' -v`
Expected: FAIL — undefined `SynthesizeAsyncImageUsage`, `AsyncSynthConfig`, etc.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/service/openai_images_usage_synth.go`:

```go
package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// AsyncSynthConfig holds the size×quality token table used to synthesize usage
// for async image tasks (which return no token usage).
type AsyncSynthConfig struct {
	OutputTokenTable map[string]map[string]int // tier ("1K"/"2K"/"4K") -> quality -> output tokens
	RefImageTokens   map[string]int            // tier -> image-input tokens per reference image
}

// AsyncSynthInput is the request information needed to synthesize usage.
type AsyncSynthInput struct {
	Size      string
	Quality   string
	N         int
	Prompt    string
	RefImages []OpenAIImagesUpload // edits: reference images (Width/Height used for tier)
	RefURLTiers []string           // edits via JSON image_url: pre-classified tiers (optional)
}

func asyncToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

// ParseAsyncSynthConfig extracts the synth config from a credentials/features map
// under key "async_image_synth". Returns ok=false if absent/malformed.
func ParseAsyncSynthConfig(m map[string]any) (AsyncSynthConfig, bool) {
	if m == nil {
		return AsyncSynthConfig{}, false
	}
	raw, ok := m["async_image_synth"].(map[string]any)
	if !ok {
		return AsyncSynthConfig{}, false
	}
	cfg := AsyncSynthConfig{
		OutputTokenTable: map[string]map[string]int{},
		RefImageTokens:   map[string]int{},
	}
	if tbl, ok := raw["output_token_table"].(map[string]any); ok {
		for tier, qv := range tbl {
			qm, ok := qv.(map[string]any)
			if !ok {
				continue
			}
			row := map[string]int{}
			for q, val := range qm {
				row[strings.ToLower(q)] = asyncToInt(val)
			}
			cfg.OutputTokenTable[strings.ToUpper(tier)] = row
		}
	}
	if rt, ok := raw["ref_image_tokens"].(map[string]any); ok {
		for tier, val := range rt {
			cfg.RefImageTokens[strings.ToUpper(tier)] = asyncToInt(val)
		}
	}
	return cfg, true
}

func normalizeAsyncQuality(q string) string {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "low":
		return "low"
	case "high":
		return "high"
	default: // medium, auto, "", unknown → medium
		return "medium"
	}
}

func estimateTextTokens(prompt string) int {
	n := (len(prompt) + 3) / 4 // ~4 chars/token, rounded up
	if n < 1 {
		n = 1
	}
	return n
}

func maxIntInRow(row map[string]int) int {
	max := 0
	for _, v := range row {
		if v > max {
			max = v
		}
	}
	return max
}

func uploadSizeString(up OpenAIImagesUpload) string {
	if up.Width > 0 && up.Height > 0 {
		return fmt.Sprintf("%dx%d", up.Width, up.Height)
	}
	return ""
}

// SynthesizeAsyncImageUsage builds an OpenAIUsage from (size, quality, refs, prompt, n).
// ok=false means a missing table cell forced a conservative row-max fallback.
func SynthesizeAsyncImageUsage(in AsyncSynthInput, cfg AsyncSynthConfig) (OpenAIUsage, bool) {
	q := normalizeAsyncQuality(in.Quality)
	tier := NormalizeImageBillingTierOrDefault(in.Size)
	row := cfg.OutputTokenTable[tier]

	outPer := 0
	if row != nil {
		outPer = row[q]
	}
	exact := true
	if outPer == 0 {
		outPer = maxIntInRow(row)
		exact = false
	}
	n := in.N
	if n <= 0 {
		n = 1
	}
	outputTokens := outPer * n

	refTok := 0
	for _, up := range in.RefImages {
		rt := tier // default to request tier if dims unknown
		if s := uploadSizeString(up); s != "" {
			rt = NormalizeImageBillingTierOrDefault(s)
		}
		refTok += cfg.RefImageTokens[rt]
	}
	for _, rt := range in.RefURLTiers {
		refTok += cfg.RefImageTokens[strings.ToUpper(rt)]
	}
	imageInAdjusted := int(math.Round(float64(refTok) * 1.6)) // 1.6 = 12.8/8

	return OpenAIUsage{
		InputTokens:  estimateTextTokens(in.Prompt) + imageInAdjusted,
		OutputTokens: outputTokens, // image output billed at output_price
	}, exact
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/service/ -run 'TestSynthesize|TestParseAsyncSynthConfig' -v`
Expected: PASS (all 6).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_usage_synth.go backend/internal/service/openai_images_usage_synth_test.go
git commit -m "feat(images-async): size×quality usage synthesizer + config parsing"
```

---

## Task 3: Request translation (OpenAI request → 12ai task submit body)

**Files:**
- Modify: `backend/internal/service/openai_images_async.go`
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildAsyncSubmitBody_Generation(t *testing.T) {
	parsed := &OpenAIImagesRequest{Prompt: "a cat", Size: "1024x1024", Quality: "auto", N: 1}
	b, err := buildAsyncSubmitBody("gpt-image-2", parsed)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "gpt-image-2" {
		t.Fatalf("model = %v", m["model"])
	}
	in := m["input"].(map[string]any)
	if in["quality"] != "medium" { // auto → medium
		t.Fatalf("quality = %v, want medium", in["quality"])
	}
	if _, hasImages := in["images"]; hasImages {
		t.Fatal("generation should not include images")
	}
}

func TestBuildAsyncSubmitBody_EditWithUpload(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Prompt: "make it red", Size: "1024x1024", Quality: "high", N: 1,
		Uploads:    []OpenAIImagesUpload{{ContentType: "image/png", Data: []byte{1, 2, 3}}},
		MaskUpload: &OpenAIImagesUpload{ContentType: "image/png", Data: []byte{4, 5}},
	}
	b, _ := buildAsyncSubmitBody("gpt-image-2", parsed)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	in := m["input"].(map[string]any)
	imgs := in["images"].([]any)
	if len(imgs) != 1 || !strings.HasPrefix(imgs[0].(string), "data:image/png;base64,") {
		t.Fatalf("images[0] = %v", imgs[0])
	}
	if mask, _ := in["mask"].(string); !strings.HasPrefix(mask, "data:image/png;base64,") {
		t.Fatalf("mask = %v", in["mask"])
	}
}
```

(add imports `encoding/json`, `strings` to the test file)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestBuildAsyncSubmitBody -v`
Expected: FAIL — `buildAsyncSubmitBody` undefined.

- [ ] **Step 3: Write minimal implementation** (append to `openai_images_async.go`)

```go
import (
	"encoding/base64"
	"encoding/json"
	// (keep strconv, strings)
)

func asyncDataURI(contentType string, data []byte) string {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "image/png"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// buildAsyncSubmitBody translates a parsed OpenAI images request into a 12ai
// task-submit payload: {"model":..., "input":{prompt,size,quality,n,images,mask}}.
func buildAsyncSubmitBody(model string, parsed *OpenAIImagesRequest) ([]byte, error) {
	n := parsed.N
	if n <= 0 {
		n = 1
	}
	input := map[string]any{
		"prompt":  parsed.Prompt,
		"size":    parsed.Size,
		"quality": normalizeAsyncQuality(parsed.Quality),
		"n":       n,
	}
	images := make([]string, 0, len(parsed.Uploads)+len(parsed.InputImageURLs))
	for _, up := range parsed.Uploads {
		images = append(images, asyncDataURI(up.ContentType, up.Data))
	}
	images = append(images, parsed.InputImageURLs...)
	if len(images) > 0 {
		input["images"] = images
	}
	if parsed.MaskUpload != nil {
		input["mask"] = asyncDataURI(parsed.MaskUpload.ContentType, parsed.MaskUpload.Data)
	} else if strings.TrimSpace(parsed.MaskImageURL) != "" {
		input["mask"] = parsed.MaskImageURL
	}
	return json.Marshal(map[string]any{"model": model, "input": input})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestBuildAsyncSubmitBody -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): translate OpenAI image request to 12ai task body"
```

---

## Task 4: Response translation (b64 outputs → OpenAI images JSON)

**Files:**
- Modify: `backend/internal/service/openai_images_async.go`
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildOpenAIImagesResponse(t *testing.T) {
	b, err := buildOpenAIImagesResponse([]string{"AAA", "BBB"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	data := m["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data len = %d", len(data))
	}
	if data[0].(map[string]any)["b64_json"] != "AAA" {
		t.Fatalf("b64_json[0] = %v", data[0])
	}
	if _, ok := m["created"]; !ok {
		t.Fatal("missing created")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestBuildOpenAIImagesResponse -v`
Expected: FAIL — `buildOpenAIImagesResponse` undefined.

- [ ] **Step 3: Write minimal implementation** (append; add `"time"` import)

```go
// buildOpenAIImagesResponse builds an OpenAI images response body from base64 images.
func buildOpenAIImagesResponse(b64s []string) ([]byte, error) {
	data := make([]map[string]any, 0, len(b64s))
	for _, b := range b64s {
		data = append(data, map[string]any{"b64_json": b})
	}
	return json.Marshal(map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestBuildOpenAIImagesResponse -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): build OpenAI b64_json response from outputs"
```

---

## Task 5: AsyncImageClient.Submit

**Files:**
- Modify: `backend/internal/service/openai_images_async.go`
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing test**

```go
import "net/http/httptest" // add to test imports (with net/http, context)

func TestAsyncClientSubmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/task/submit" || r.Header.Get("Authorization") != "Bearer sk-x" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"id":"task_123","status":"queued"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	id, err := cl.Submit(context.Background(), []byte(`{}`))
	if err != nil || id != "task_123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestAsyncClientSubmitNoID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	if _, err := cl.Submit(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error for missing id")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestAsyncClientSubmit -v`
Expected: FAIL — `AsyncImageClient` undefined.

- [ ] **Step 3: Write minimal implementation** (append; add `bytes`, `context`, `fmt`, `io`, `net/http` imports)

```go
type AsyncImageClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

type asyncSubmitResp struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (cl *AsyncImageClient) Submit(ctx context.Context, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cl.baseURL+"/v1/task/submit", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cl.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("submit status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var sr asyncSubmitResp
	if err := json.Unmarshal(rb, &sr); err != nil {
		return "", fmt.Errorf("submit parse: %w", err)
	}
	if strings.TrimSpace(sr.ID) == "" {
		return "", fmt.Errorf("submit returned no task id")
	}
	return sr.ID, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestAsyncClientSubmit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): AsyncImageClient.Submit"
```

---

## Task 6: AsyncImageClient.Poll (statuses, retries, timeout, cancel)

**Files:**
- Modify: `backend/internal/service/openai_images_async.go`
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing tests**

```go
import "sync/atomic" // add; also "time", "errors"

func TestAsyncClientPollCompletes(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			_, _ = w.Write([]byte(`{"status":"in_progress"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed","outputs":["https://img/x.png"]}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	pr, err := cl.Poll(context.Background(), "task_1", 5*time.Millisecond, 2*time.Second)
	if err != nil || len(pr.Outputs) != 1 {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
}

func TestAsyncClientPollFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","error":"nsfw"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	pr, err := cl.Poll(context.Background(), "t", 5*time.Millisecond, time.Second)
	if err != nil || !pr.Failed || pr.ErrMsg != "nsfw" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
}

func TestAsyncClientPollTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"in_progress"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	_, err := cl.Poll(context.Background(), "t", 5*time.Millisecond, 30*time.Millisecond)
	if !errors.Is(err, errAsyncTimeout) {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestAsyncClientPollCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"in_progress"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cl.Poll(ctx, "t", 5*time.Millisecond, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && go test ./internal/service/ -run TestAsyncClientPoll -v`
Expected: FAIL — `Poll` / `errAsyncTimeout` undefined.

- [ ] **Step 3: Write minimal implementation** (append; add `errors`, `time`)

```go
var errAsyncTimeout = errors.New("async image task timed out")

type AsyncPollResult struct {
	Outputs []string
	Failed  bool
	ErrMsg  string
}

type asyncTaskResp struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Outputs []string `json:"outputs"`
	Error   string   `json:"error"`
}

func (cl *AsyncImageClient) getTask(ctx context.Context, taskID string) (*asyncTaskResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cl.baseURL+"/v1/task/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cl.apiKey)
	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var tr asyncTaskResp
	if err := json.Unmarshal(rb, &tr); err != nil {
		return nil, fmt.Errorf("poll parse: %w", err)
	}
	return &tr, nil
}

// Poll polls the task until terminal status, ctx cancel, or maxWait. Up to 3
// consecutive transient poll errors are tolerated before aborting.
func (cl *AsyncImageClient) Poll(ctx context.Context, taskID string, interval, maxWait time.Duration) (*AsyncPollResult, error) {
	deadline := time.Now().Add(maxWait)
	consecFail := 0
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, errAsyncTimeout
		}
		tr, err := cl.getTask(ctx, taskID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			consecFail++
			if consecFail >= 3 {
				return nil, err
			}
		} else {
			consecFail = 0
			switch strings.ToLower(tr.Status) {
			case "completed":
				return &AsyncPollResult{Outputs: tr.Outputs}, nil
			case "partial_completed":
				return &AsyncPollResult{Outputs: tr.Outputs, ErrMsg: tr.Error}, nil
			case "failed":
				return &AsyncPollResult{Failed: true, ErrMsg: tr.Error}, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && go test ./internal/service/ -run TestAsyncClientPoll -v`
Expected: PASS (all 4).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): AsyncImageClient.Poll with retries/timeout/cancel"
```

---

## Task 7: AsyncImageClient.FetchAsB64

**Files:**
- Modify: `backend/internal/service/openai_images_async.go`
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestAsyncClientFetchAsB64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xDE, 0xAD})
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	out, err := cl.FetchAsB64(context.Background(), []string{srv.URL + "/a.png"})
	if err != nil || len(out) != 1 || out[0] != "3q0=" { // base64(0xDEAD)
		t.Fatalf("out=%v err=%v", out, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestAsyncClientFetchAsB64 -v`
Expected: FAIL — `FetchAsB64` undefined.

- [ ] **Step 3: Write minimal implementation** (append)

```go
func (cl *AsyncImageClient) FetchAsB64(ctx context.Context, urls []string) ([]string, error) {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := cl.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download %s status %d", u, resp.StatusCode)
		}
		out = append(out, base64.StdEncoding.EncodeToString(b))
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/service/ -run TestAsyncClientFetchAsB64 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): AsyncImageClient.FetchAsB64"
```

---

## Task 8: ForwardImagesAsync orchestration + branch wiring

**Files:**
- Modify: `backend/internal/service/openai_images_async.go`
- Modify: `backend/internal/service/openai_images.go` (branch ~line 578)
- Test: `backend/internal/service/openai_images_async_test.go`

- [ ] **Step 1: Write the failing test** (orchestration against a mock 12ai, asserting written body + synthesized usage)

```go
func TestForwardImagesAsync_EndToEnd(t *testing.T) {
	// mock 12ai: submit -> task id; poll -> completed; image download -> bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/task/submit":
			_, _ = w.Write([]byte(`{"id":"task_1","status":"queued"}`))
		case strings.HasPrefix(r.URL.Path, "/v1/task/"):
			_, _ = w.Write([]byte(`{"status":"completed","outputs":["` + "PLACEHOLDER_IMG" + `"]}`))
		default:
			_, _ = w.Write([]byte{0x01})
		}
	}))
	defer srv.Close()

	// account with async config + synth table in credentials
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"async_enabled":  "true",
			"async_base_url": srv.URL,
			"api_key":        "sk-x",
			"async_image_synth": map[string]any{
				"output_token_table": map[string]any{"1K": map[string]any{"high": float64(7023)}},
			},
		},
	}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "x", Size: "1024x1024", SizeTier: "1K", Quality: "high", N: 1}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	s := &OpenAIGatewayService{} // construct with whatever zero-deps are needed for this path

	result, err := s.ForwardImagesAsync(context.Background(), c, account, parsed, "gpt-image-2", "gpt-image-2", time.Now())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Usage.OutputTokens != 7023 {
		t.Fatalf("synth output tokens = %d, want 7023", result.Usage.OutputTokens)
	}
	if result.ImageCount != 1 {
		t.Fatalf("image count = %d", result.ImageCount)
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"b64_json"`) {
		t.Fatalf("client response not written: %d %s", w.Code, w.Body.String())
	}
}
```

> Replace the `"PLACEHOLDER_IMG"` output URL with `srv.URL+"/img.png"` (the handler's default case returns image bytes). Build `s` via the package's existing test constructor/zero value — check a sibling `_test.go` in `internal/service` for how `OpenAIGatewayService` is instantiated in tests; `ForwardImagesAsync` only touches `account`, the client, and `c`, so a minimal struct suffices. Add `"github.com/gin-gonic/gin"` to test imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/service/ -run TestForwardImagesAsync_EndToEnd -v`
Expected: FAIL — `ForwardImagesAsync` undefined.

- [ ] **Step 3: Write minimal implementation** (append to `openai_images_async.go`; add `context`, `gin`, `net/http`, `time`)

```go
import (
	"github.com/gin-gonic/gin"
	// plus context, net/http, time already used
)

func (s *OpenAIGatewayService) newAsyncImageClient(account *Account) *AsyncImageClient {
	return &AsyncImageClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    account.AsyncImageBaseURL(),
		apiKey:     account.GetOpenAIApiKey(),
	}
}

// asyncFail writes a clean client error and returns a typed error that the
// handler maps to a non-failover, non-billed error response.
func asyncFail(c *gin.Context, status int, msg string) error {
	e := &OpenAIImagesUpstreamError{
		StatusCode: status,
		ErrorType:  "image_generation_user_error",
		Message:    sanitizeUpstreamErrorMessage(msg),
	}
	if c != nil {
		writeOpenAIImagesUpstreamErrorResponse(c, e)
	}
	return e
}

// ForwardImagesAsync submits the request to the async task upstream, polls to
// completion, writes a b64_json response, and returns a result whose Usage is
// synthesized for token billing. The request ctx must be cancelable so client
// disconnects stop polling (no billing).
func (s *OpenAIGatewayService) ForwardImagesAsync(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	requestModel, upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	cl := s.newAsyncImageClient(account)

	body, err := buildAsyncSubmitBody(upstreamModel, parsed)
	if err != nil {
		return nil, asyncFail(c, http.StatusBadRequest, "build async request: "+err.Error())
	}

	submitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	taskID, err := cl.Submit(submitCtx, body)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, asyncFail(c, http.StatusBadGateway, "async submit failed: "+err.Error())
	}

	interval := time.Duration(account.AsyncPollIntervalMs()) * time.Millisecond
	maxWait := time.Duration(account.AsyncMaxWaitMs()) * time.Millisecond
	pr, err := cl.Poll(ctx, taskID, interval, maxWait)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err() // client disconnect: no response, no billing
		case errors.Is(err, errAsyncTimeout):
			return nil, asyncFail(c, http.StatusGatewayTimeout, "image task timed out")
		default:
			return nil, asyncFail(c, http.StatusBadGateway, err.Error())
		}
	}
	if pr.Failed || len(pr.Outputs) == 0 {
		msg := "image task failed"
		if pr.ErrMsg != "" {
			msg += ": " + pr.ErrMsg
		}
		return nil, asyncFail(c, http.StatusBadGateway, msg)
	}

	b64s, err := cl.FetchAsB64(ctx, pr.Outputs)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, asyncFail(c, http.StatusBadGateway, "image download failed: "+err.Error())
	}
	delivered := len(b64s)

	respJSON, err := buildOpenAIImagesResponse(b64s)
	if err != nil {
		return nil, asyncFail(c, http.StatusInternalServerError, err.Error())
	}
	c.Data(http.StatusOK, "application/json", respJSON)

	synthCfg, _ := ParseAsyncSynthConfig(account.Credentials)
	usage, exact := SynthesizeAsyncImageUsage(AsyncSynthInput{
		Size:      parsed.Size,
		Quality:   parsed.Quality,
		N:         delivered,
		Prompt:    parsed.Prompt,
		RefImages: parsed.Uploads,
	}, synthCfg)
	if !exact {
		logger.LegacyPrintf("service.openai_gateway",
			"[OpenAI] async image synth fell back to row-max account=%d size=%s quality=%s",
			account.ID, parsed.Size, parsed.Quality)
	}

	return &OpenAIForwardResult{
		Usage:          usage,
		Model:          requestModel,
		UpstreamModel:  upstreamModel,
		Duration:       time.Since(startTime),
		ImageCount:     delivered,
		ImageSize:      parsed.SizeTier,
		ImageInputSize: parsed.Size,
	}, nil
}
```

> Add `"errors"` and the `logger` import (match the existing alias used in `openai_images.go`, e.g. `"github.com/Wei-Shaw/sub2api/internal/pkg/logger"` — copy the exact import path from the top of `openai_images.go`).

- [ ] **Step 4: Add the branch in `forwardOpenAIImagesAPIKey`**

In `backend/internal/service/openai_images.go`, immediately after `upstreamModel` is validated (after the `validateOpenAIImagesModel(upstreamModel)` block, ~line 578), insert:

```go
	if account.IsAsyncImage() {
		return s.ForwardImagesAsync(ctx, c, account, parsed, requestModel, upstreamModel, startTime)
	}
```

> Use `ctx` (NOT the detached `upstreamCtx`) so client disconnects cancel polling. `startTime`, `requestModel`, `upstreamModel`, `parsed`, `account`, `c` are all already in scope here.

- [ ] **Step 5: Run tests + build**

Run: `cd backend && go build ./... && go test ./internal/service/ -run 'TestForwardImagesAsync|TestAsyncClient|TestSynthesize|TestBuildAsync|TestBuildOpenAIImagesResponse|TestAccountAsync' -v`
Expected: build OK; all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/openai_images_async.go backend/internal/service/openai_images.go backend/internal/service/openai_images_async_test.go
git commit -m "feat(images-async): ForwardImagesAsync orchestration + branch wiring"
```

---

## Task 9: Regression + full build/test

**Files:** none new (verification task)

- [ ] **Step 1: Add a regression test asserting async-disabled is inert**

Append to `openai_images_async_test.go`:

```go
func TestIsAsyncImageDisabledByDefault(t *testing.T) {
	a := &Account{Credentials: map[string]any{"api_key": "sk-x", "base_url": "https://new.12ai.org/v1"}}
	if a.IsAsyncImage() {
		t.Fatal("account without async_enabled must not be async (sync path unchanged)")
	}
}
```

- [ ] **Step 2: Run full service tests + vet**

Run: `cd backend && go vet ./internal/service/ && go test ./internal/service/ -run Async -v && go build ./...`
Expected: PASS, build OK. (`go vet` catches unused imports / shadowing.)

- [ ] **Step 3: Run the wider suite the change could touch**

Run: `cd backend && go test ./internal/service/... ./internal/handler/...`
Expected: PASS. If pre-existing failures are unrelated to images, note them; do not "fix" unrelated tests.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/openai_images_async_test.go
git commit -m "test(images-async): regression — async disabled leaves sync path inert"
```

---

## Task 10: Production config + calibration (operational; no app code)

> These are DB/config changes on the server, not code. Run after the image is deployed. They mirror CLAUDE.md conventions (direct SQL, restart, redis invalidate).

- [ ] **Step 1: Calibrate the 2K/4K token table.**
  Run async generations for `{2048x2048, 3840x2160} × {low,medium,high}` and edits with 2K/4K reference images, read each task's net charge from the 12ai dashboard (`net ÷ ¥48e-6 = effective output tokens`; ref-image tokens from the dashboard input breakdown). Record the 6 output cells + 2K/4K `ref_image_tokens`. 1K is known: `{low:196, medium:1756, high:7023}`, `ref_image_tokens.1K=1024`.

- [ ] **Step 2: Configure account 1131 (async credentials + synth table).**

```sql
UPDATE accounts SET credentials = jsonb_set(
  jsonb_set(
    jsonb_set(
      jsonb_set(credentials::jsonb, '{async_enabled}', '"true"'),
      '{async_base_url}', '"https://cdn.12ai.org"'),
    '{api_key}', '"<ASYNC_KEY_WITH_TASK_PERMISSION>"'),
  '{async_image_synth}', '{
     "output_token_table": {
       "1K": {"low":196,"medium":1756,"high":7023},
       "2K": {"low":CAL,"medium":CAL,"high":CAL},
       "4K": {"low":CAL,"medium":CAL,"high":CAL}
     },
     "ref_image_tokens": {"1K":1024,"2K":CAL,"4K":CAL}
   }'::jsonb)
WHERE id = 1131;
```
(replace `CAL` with calibrated values; ensure `base_url` stays the sync endpoint.)

- [ ] **Step 3: Ensure channel 13 rates are token mode.**

```sql
UPDATE channel_model_pricing
SET billing_mode='token', input_price=0.000008, output_price=0.000048, image_output_price=0
WHERE channel_id=13 AND models::text LIKE '%gpt-image-2%';
```

- [ ] **Step 4: Set group 21 margin = 1.5.**

```sql
UPDATE groups SET rate_multiplier=1.5 WHERE id=21;
```

- [ ] **Step 5: Restart + invalidate cache (CLAUDE.md gotcha #3).**

```bash
ssh ssh-tokyo 'cd /opt/stack && docker compose restart sub2api'
# if the test api key's group/binding changed:
ssh ssh-tokyo "docker exec sub2api-redis redis-cli PUBLISH auth:cache:invalidate '<full-key-string>'"
```

- [ ] **Step 6: Live verify.** From Infinite-Canvas (or curl) run a 1K-high edit through the gateway; confirm 200 + image, no 524, and that the user's usage log shows `output_tokens≈7023` and cost ≈ `¥0.337 × 1.5 = ¥0.506`. Confirm a `failed`/oversize request is NOT billed.

---

## Self-Review notes (author)

- **Spec coverage:** §3 decisions → Tasks 1–8; §6 synthesis → Task 2; §8 lifecycle → Tasks 6, 8; §5 config → Tasks 1, 10; §7 billing validation → Task 10 step 6; §9 testing → Tasks 2–9; §10 open items → Task 10 steps 1.
- **Deviation:** synth token table stored in `account.Credentials` (not `channel.features_config`) for plumbing — flagged for sign-off.
- **Type consistency:** `AsyncImageClient{httpClient,baseURL,apiKey}`, `AsyncPollResult{Outputs,Failed,ErrMsg}`, `AsyncSynthConfig{OutputTokenTable,RefImageTokens}`, `AsyncSynthInput{Size,Quality,N,Prompt,RefImages,RefURLTiers}` used consistently across tasks.
- **Confirm-on-implement:** exact `logger` import path + platform constant name (`PlatformOpenAI`) + `OpenAIGatewayService` test construction — all called out inline.
