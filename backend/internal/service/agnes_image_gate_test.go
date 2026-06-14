package service

import "testing"

func TestAccountImageModelsAndGate(t *testing.T) {
	a := &Account{Credentials: map[string]any{
		"image_models": "agnes-image-2.0-flash, agnes-image-2.1-flash",
	}}
	got := a.ImageModels()
	if len(got) != 2 || got[0] != "agnes-image-2.0-flash" || got[1] != "agnes-image-2.1-flash" {
		t.Fatalf("ImageModels parse wrong: %#v", got)
	}
	if !a.IsDeclaredImageModel("agnes-image-2.0-flash") {
		t.Fatal("declared model should match")
	}
	if !a.IsDeclaredImageModel("AGNES-IMAGE-2.1-FLASH") {
		t.Fatal("match should be case-insensitive")
	}
	if a.IsDeclaredImageModel("gpt-5.4") {
		t.Fatal("undeclared model must not match")
	}
	if err := validateOpenAIImagesModelForAccount("gpt-image-2", a); err != nil {
		t.Fatalf("gpt-image-2 must pass: %v", err)
	}
	if err := validateOpenAIImagesModelForAccount("agnes-image-2.0-flash", a); err != nil {
		t.Fatalf("declared agnes image must pass: %v", err)
	}
	if err := validateOpenAIImagesModelForAccount("gpt-5.4", a); err == nil {
		t.Fatal("non-image model must fail")
	}

	// nil account: declared-model path unavailable, must still reject non-gpt-image-*
	if err := validateOpenAIImagesModelForAccount("agnes-image-2.0-flash", nil); err == nil {
		t.Fatal("nil account: undeclared model must fail")
	}

	// empty image_models credential
	aEmpty := &Account{Credentials: map[string]any{"image_models": ""}}
	if ms := aEmpty.ImageModels(); len(ms) != 0 {
		t.Fatalf("empty image_models: want len 0, got %#v", ms)
	}
	if aEmpty.IsDeclaredImageModel("anything") {
		t.Fatal("empty image_models: IsDeclaredImageModel must return false")
	}

	// whitespace-only entries must be filtered out
	aWhitespace := &Account{Credentials: map[string]any{"image_models": " , ,  "}}
	if ms := aWhitespace.ImageModels(); len(ms) != 0 {
		t.Fatalf("whitespace-only entries: want len 0, got %#v", ms)
	}
}
