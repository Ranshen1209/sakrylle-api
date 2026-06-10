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
	if u.InputTokens < 1638 { // 1024 * 1.6 = 1638, plus tiny text input
		t.Fatalf("edit input tokens = %d, want >= 1638", u.InputTokens)
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

func TestSynthesizeUsesConfiguredImageInputRatio(t *testing.T) {
	cfg := AsyncSynthConfig{
		OutputTokenTable: map[string]map[string]int{"1K": {"medium": 1756}},
		RefImageTokens:   map[string]int{"1K": 1000},
		ImageInputRatio:  2.0,
	}
	u, _ := SynthesizeAsyncImageUsage(AsyncSynthInput{
		Size: "1024x1024", Quality: "medium", N: 1,
		RefImages: []OpenAIImagesUpload{{Width: 1024, Height: 1024}},
	}, cfg)
	if u.InputTokens < 2000 || u.InputTokens > 2010 { // 1000 * 2.0 = 2000 (+ tiny text)
		t.Fatalf("expected ~2000 input tokens (ratio 2.0), got %d", u.InputTokens)
	}
}

func TestSynthesizeDefaultsRatioWhenUnset(t *testing.T) {
	cfg := AsyncSynthConfig{ // no ImageInputRatio → default 1.6
		OutputTokenTable: map[string]map[string]int{"1K": {"medium": 1756}},
		RefImageTokens:   map[string]int{"1K": 1000},
	}
	u, _ := SynthesizeAsyncImageUsage(AsyncSynthInput{
		Size: "1024x1024", Quality: "medium", N: 1,
		RefImages: []OpenAIImagesUpload{{Width: 1024, Height: 1024}},
	}, cfg)
	if u.InputTokens < 1600 || u.InputTokens > 1610 { // 1000 * 1.6 = 1600 (+ tiny text)
		t.Fatalf("expected ~1600 input tokens (default 1.6), got %d", u.InputTokens)
	}
}

func TestParseAsyncSynthConfigImageInputRatio(t *testing.T) {
	withRatio := map[string]any{"async_image_synth": map[string]any{"image_input_ratio": float64(2.0)}}
	cfg, _ := ParseAsyncSynthConfig(withRatio)
	if cfg.ImageInputRatio != 2.0 {
		t.Fatalf("ratio = %v, want 2.0", cfg.ImageInputRatio)
	}
	without := map[string]any{"async_image_synth": map[string]any{}}
	cfg2, _ := ParseAsyncSynthConfig(without)
	if cfg2.ImageInputRatio != 1.6 {
		t.Fatalf("default ratio = %v, want 1.6", cfg2.ImageInputRatio)
	}
}
