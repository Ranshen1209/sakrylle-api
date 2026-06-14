package service

import (
	"fmt"
	"strings"
)

// ImageModels returns the model names this account declares as image-generation
// models via the comma-separated `image_models` credential. Used to let
// non-"gpt-image-" upstreams (e.g. Agnes) pass the images-endpoint model gate.
func (a *Account) ImageModels() []string {
	if a == nil {
		return nil
	}
	raw := strings.TrimSpace(a.GetCredential("image_models"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsDeclaredImageModel reports whether model is in this account's image_models
// allowlist (case-insensitive, trimmed).
func (a *Account) IsDeclaredImageModel(model string) bool {
	if a == nil {
		return false
	}
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" {
		return false
	}
	for _, m := range a.ImageModels() {
		if strings.ToLower(m) == model {
			return true
		}
	}
	return false
}

// validateOpenAIImagesModelForAccount accepts gpt-image-* models OR models the
// account explicitly declares as image models; otherwise returns the same error
// shape as validateOpenAIImagesModel.
func validateOpenAIImagesModelForAccount(model string, account *Account) error {
	model = strings.TrimSpace(model)
	if isOpenAIImageGenerationModel(model) {
		return nil
	}
	if account != nil && account.IsDeclaredImageModel(model) {
		return nil
	}
	if model == "" {
		return fmt.Errorf("images endpoint requires an image model")
	}
	return fmt.Errorf("images endpoint requires an image model, got %q", model)
}
