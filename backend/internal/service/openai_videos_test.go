package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestParseVideosRequestAndUsageSynth(t *testing.T) {
	body := []byte(`{"model":"agnes-video-v2.0","prompt":"a cat","size":"1280x768"}`)
	req, err := ParseOpenAIVideosRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "agnes-video-v2.0" || req.Prompt != "a cat" {
		t.Fatalf("parsed wrong: %#v", req)
	}
	if _, err := ParseOpenAIVideosRequest([]byte(`{"prompt":"x"}`)); err == nil {
		t.Fatal("empty model must be rejected")
	}
	if _, err := ParseOpenAIVideosRequest([]byte(`not json`)); err == nil {
		t.Fatal("invalid json must be rejected")
	}

	if got := synthVideoOutputTokens(10.0, 18.375); got != 10 {
		t.Fatalf("10.0s -> 10 tokens, got %d", got)
	}
	if got := synthVideoOutputTokens(10.4, 18.375); got != 10 {
		t.Fatalf("round down 10.4 -> 10, got %d", got)
	}
	if got := synthVideoOutputTokens(10.5, 18.375); got != 11 {
		t.Fatalf("round half up 10.5 -> 11, got %d", got)
	}
	if got := synthVideoOutputTokens(0, 18.375); got != 18 {
		t.Fatalf("0 -> fallback round(18.375)=18, got %d", got)
	}
}

func TestVideoClientSubmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/videos" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer KEY" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"video_id":"video_abc","status":"queued","seconds":"10.0","size":"1280x768"}`))
	}))
	defer srv.Close()

	cl := &VideoClient{httpClient: &http.Client{Timeout: 5 * time.Second}, baseURL: srv.URL, apiKey: "KEY", submitPath: "/v1/videos"}
	sub, err := cl.Submit(context.Background(), []byte(`{"model":"agnes-video-v2.0","prompt":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if sub.VideoID != "video_abc" {
		t.Fatalf("video_id = %q", sub.VideoID)
	}

	bad := &VideoClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "WRONG", submitPath: "/v1/videos"}
	if _, err := bad.Submit(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("401 must error")
	}
}

func TestAccountVideoConfig(t *testing.T) {
	a := &Account{Credentials: map[string]any{
		"video_enabled":          "true",
		"video_models":           "agnes-video-v2.0",
		"video_submit_path":      "/v1/videos",
		"video_poll_path":        "/agnesapi",
		"video_poll_interval_ms": "5000",
		"video_max_wait_ms":      "600000",
		"video_default_seconds":  "18.375",
		"video_host_suffix":      "agnes-ai.com, googleapis.com",
	}}
	if !a.IsVideoEnabled() {
		t.Fatal("video should be enabled")
	}
	if !a.IsDeclaredVideoModel("agnes-video-v2.0") {
		t.Fatal("declared video model should match")
	}
	if a.VideoSubmitPath() != "/v1/videos" || a.VideoPollPath() != "/agnesapi" {
		t.Fatalf("paths wrong: %s %s", a.VideoSubmitPath(), a.VideoPollPath())
	}
	if a.VideoPollIntervalMs() != 5000 || a.VideoMaxWaitMs() != 600000 {
		t.Fatalf("intervals wrong: %d %d", a.VideoPollIntervalMs(), a.VideoMaxWaitMs())
	}
	if a.VideoDefaultSeconds() != 18.375 {
		t.Fatalf("default seconds wrong: %v", a.VideoDefaultSeconds())
	}
	if hs := a.VideoHostSuffixes(); len(hs) != 2 || hs[0] != "agnes-ai.com" || hs[1] != "googleapis.com" {
		t.Fatalf("host suffixes wrong: %#v", hs)
	}
	b := &Account{Credentials: map[string]any{}}
	if b.IsVideoEnabled() {
		t.Fatal("absent -> disabled")
	}
	if b.VideoSubmitPath() != "/v1/videos" || b.VideoPollPath() != "/agnesapi" {
		t.Fatal("absent -> default paths")
	}
	if b.VideoPollIntervalMs() != 5000 || b.VideoMaxWaitMs() != 600000 {
		t.Fatal("absent -> default intervals")
	}
	if b.VideoDefaultSeconds() != 18.375 {
		t.Fatal("absent -> default seconds")
	}
}

func TestVideoURLValidationAndResponse(t *testing.T) {
	if err := validateVideoURL("https://storage.googleapis.com/x.mp4", []string{"googleapis.com"}); err != nil {
		t.Fatalf("allowed host must pass: %v", err)
	}
	if err := validateVideoURL("https://evil.com/x.mp4", []string{"googleapis.com"}); err == nil {
		t.Fatal("disallowed host must fail")
	}
	if err := validateVideoURL("ftp://storage.googleapis.com/x", []string{"googleapis.com"}); err == nil {
		t.Fatal("bad scheme must fail")
	}
	if err := validateVideoURL("https://anything.com/x", nil); err != nil {
		t.Fatalf("empty allowlist allows any host: %v", err)
	}
	if err := validateVideoURL("https://evilgoogleapis.com/x.mp4", []string{"googleapis.com"}); err == nil {
		t.Fatal("suffix-only substring host must not pass")
	}
}

func TestSubmitVideoHappyPath(t *testing.T) {
	var submitHit int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/videos" {
			submitHit++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"task_abc","task_id":"task_abc","video_id":"video_x","status":"queued","seconds":"3.4","size":"1280x704"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	account := &Account{ID: 1137, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": srv.URL + "/v1", "api_key": "KEY",
		"video_enabled": "true", "video_models": "agnes-video-v2.0",
		"video_submit_path": "/videos", "video_default_seconds": "18.375",
		"video_host_suffix": "agnes-ai.space",
	}}
	s := &OpenAIGatewayService{}
	c, rec := newVideosTestCtx(t)
	parsed := &OpenAIVideosRequest{Model: "agnes-video-v2.0", Prompt: "x", Raw: []byte(`{"model":"agnes-video-v2.0","prompt":"x"}`)}

	res, err := s.SubmitVideo(context.Background(), c, account, parsed, "agnes-video-v2.0", "agnes-video-v2.0", time.Now())
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if submitHit != 1 {
		t.Fatalf("submitHit=%d", submitHit)
	}
	if rec.Code != 200 || gjson.GetBytes(rec.Body.Bytes(), "status").String() != "queued" ||
		gjson.GetBytes(rec.Body.Bytes(), "id").String() != "task_abc" {
		t.Fatalf("queued body wrong: %s", rec.Body.String())
	}
	if res.Usage.OutputTokens != 3 {
		t.Fatalf("expected 3 output tokens, got %d", res.Usage.OutputTokens)
	}
	if res.ImageCount != 0 {
		t.Fatalf("ImageCount must be 0, got %d", res.ImageCount)
	}
}

func TestSubmitVideoSubmitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer srv.Close()
	account := &Account{ID: 1137, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"base_url": srv.URL + "/v1", "api_key": "KEY", "video_submit_path": "/videos",
		"video_default_seconds": "18.375",
	}}
	s := &OpenAIGatewayService{}
	c, rec := newVideosTestCtx(t)
	parsed := &OpenAIVideosRequest{Model: "agnes-video-v2.0", Raw: []byte(`{"model":"agnes-video-v2.0"}`)}
	if _, err := s.SubmitVideo(context.Background(), c, account, parsed, "agnes-video-v2.0", "agnes-video-v2.0", time.Now()); err == nil {
		t.Fatal("submit 502 must error")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestVideoClientRetrieve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/videos/task_abc" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer KEY" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task_abc","status":"completed","progress":100,"seconds":"3.4","size":"1280x704","model":"agnes-video-v2.0","remixed_from_video_id":"https://platform-outputs.agnes-ai.space/x.mp4","error":null}`))
	}))
	defer srv.Close()

	cl := &VideoClient{httpClient: srv.Client(), baseURL: srv.URL + "/v1", apiKey: "KEY", submitPath: "/videos"}
	tr, err := cl.Retrieve(context.Background(), "task_abc")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != "completed" || tr.Progress != 100 || tr.Model != "agnes-video-v2.0" ||
		tr.RemixedFromVideoID != "https://platform-outputs.agnes-ai.space/x.mp4" || parseVideoSeconds(tr.Seconds) != 3.4 {
		t.Fatalf("retrieve parsed wrong: %#v", tr)
	}
	if _, err := cl.Retrieve(context.Background(), "missing"); err == nil {
		t.Fatal("404 must error")
	}
}

func TestBuildVideoStatusResponse(t *testing.T) {
	// completed
	b, err := buildVideoStatusResponse("task_1", "agnes-video-v2.0", "completed",
		"https://platform-outputs.agnes-ai.space/x.mp4", "3.4", "1280x704", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(b, "id").String() != "task_1" ||
		gjson.GetBytes(b, "object").String() != "video" ||
		gjson.GetBytes(b, "status").String() != "completed" ||
		gjson.GetBytes(b, "url").String() != "https://platform-outputs.agnes-ai.space/x.mp4" ||
		gjson.GetBytes(b, "seconds").String() != "3.4" ||
		gjson.GetBytes(b, "size").String() != "1280x704" {
		t.Fatalf("completed shape wrong: %s", b)
	}
	// in_progress: no url, has progress
	b2, _ := buildVideoStatusResponse("task_1", "agnes-video-v2.0", "in_progress", "", "3.4", "1280x704", 30, "")
	if gjson.GetBytes(b2, "url").Exists() {
		t.Fatalf("in_progress must omit url: %s", b2)
	}
	if gjson.GetBytes(b2, "progress").Int() != 30 {
		t.Fatalf("in_progress progress wrong: %s", b2)
	}
	// failed: has error, no url
	b3, _ := buildVideoStatusResponse("task_1", "agnes-video-v2.0", "failed", "", "", "", 0, "boom")
	if gjson.GetBytes(b3, "status").String() != "failed" || gjson.GetBytes(b3, "error").String() != "boom" {
		t.Fatalf("failed shape wrong: %s", b3)
	}
	if gjson.GetBytes(b3, "url").Exists() {
		t.Fatalf("failed must omit url: %s", b3)
	}
}

func newVideosTestCtx(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return c, rec
}

func TestRetrieveVideoStates(t *testing.T) {
	makeSrv := func(payload string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(payload))
		}))
	}
	acct := func(srv *httptest.Server) *Account {
		return &Account{ID: 1137, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"base_url": srv.URL + "/v1", "api_key": "KEY",
			"video_submit_path": "/videos", "video_host_suffix": "agnes-ai.space",
		}}
	}
	s := &OpenAIGatewayService{}

	srv := makeSrv(`{"id":"task_x","status":"completed","progress":100,"seconds":"3.4","size":"1280x704","model":"agnes-video-v2.0","remixed_from_video_id":"https://platform-outputs.agnes-ai.space/x.mp4"}`)
	defer srv.Close()
	c, rec := newVideosTestCtx(t)
	if err := s.RetrieveVideo(context.Background(), c, acct(srv), "task_x"); err != nil {
		t.Fatalf("completed: %v", err)
	}
	if rec.Code != 200 || gjson.GetBytes(rec.Body.Bytes(), "url").String() == "" {
		t.Fatalf("completed body wrong: %d %s", rec.Code, rec.Body.String())
	}
	if gjson.GetBytes(rec.Body.Bytes(), "status").String() != "completed" {
		t.Fatalf("status must be normalized lowercase: %s", rec.Body.String())
	}

	srv2 := makeSrv(`{"id":"task_x","status":"in_progress","progress":40,"model":"agnes-video-v2.0"}`)
	defer srv2.Close()
	c2, rec2 := newVideosTestCtx(t)
	if err := s.RetrieveVideo(context.Background(), c2, acct(srv2), "task_x"); err != nil {
		t.Fatalf("in_progress: %v", err)
	}
	if rec2.Code != 200 || gjson.GetBytes(rec2.Body.Bytes(), "url").Exists() {
		t.Fatalf("in_progress body wrong: %s", rec2.Body.String())
	}

	srvBad := makeSrv(`{"id":"task_x","status":"completed","progress":100,"seconds":"3.4","model":"agnes-video-v2.0","remixed_from_video_id":"https://evil.com/x.mp4"}`)
	defer srvBad.Close()
	c3, rec3 := newVideosTestCtx(t)
	if err := s.RetrieveVideo(context.Background(), c3, acct(srvBad), "task_x"); err == nil {
		t.Fatal("SSRF reject must error")
	}
	if rec3.Code != http.StatusBadGateway {
		t.Fatalf("SSRF reject must be 502, got %d", rec3.Code)
	}

	// Defensive fallback: completed with only "url" field (no remixed_from_video_id).
	srvURL := makeSrv(`{"id":"task_x","status":"completed","progress":100,"seconds":"3.4","model":"agnes-video-v2.0","url":"https://platform-outputs.agnes-ai.space/y.mp4"}`)
	defer srvURL.Close()
	c4, rec4 := newVideosTestCtx(t)
	if err := s.RetrieveVideo(context.Background(), c4, acct(srvURL), "task_x"); err != nil {
		t.Fatalf("url-field fallback: %v", err)
	}
	if rec4.Code != 200 {
		t.Fatalf("url-field fallback: expected 200, got %d", rec4.Code)
	}
	if got := gjson.GetBytes(rec4.Body.Bytes(), "url").String(); got != "https://platform-outputs.agnes-ai.space/y.mp4" {
		t.Fatalf("url-field fallback: url wrong: %s", rec4.Body.String())
	}
}
