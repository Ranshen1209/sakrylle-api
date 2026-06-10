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
	Size        string
	Quality     string
	N           int
	Prompt      string
	RefImages   []OpenAIImagesUpload // edits: reference images (Width/Height used for tier)
	RefURLTiers []string             // edits via JSON image_url: pre-classified tiers (optional)
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
