package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// DeepSeek requires this beta token for Files API operations through its
// Anthropic-compatible surface and for image blocks that reference file IDs.
const deepSeekFilesAPIBetaToken = "files-api-2025-04-14"

const deepSeekFilesMaxIDLength = 256

// validateDeepSeekFileID rejects path separators and control characters before
// an ID is appended to an upstream URL. DeepSeek IDs are opaque to the gateway,
// but the documented file-api-* form is deliberately restricted to URL-safe
// characters so a client cannot turn the path parameter into a traversal.
func validateDeepSeekFileID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", fmt.Errorf("file id is empty")
	}
	if len(id) > deepSeekFilesMaxIDLength {
		return "", fmt.Errorf("file id exceeds %d characters", deepSeekFilesMaxIDLength)
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '/' || r == '\\' {
			return "", fmt.Errorf("file id contains an invalid character")
		}
		if !deepSeekFileIDRuneAllowed(r) {
			return "", fmt.Errorf("file id contains an invalid character")
		}
	}
	return id, nil
}

func deepSeekFileIDRuneAllowed(r rune) bool {
	return r == '-' || r == '_' || r == '.' || r == ':' ||
		(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// ValidateDeepSeekFileID exposes the same path-safety validation used by the
// Files proxy so handlers can reject malformed IDs before selecting an
// upstream account. The normalized ID is intentionally not returned: callers
// should keep the original opaque value only after this check succeeds.
func ValidateDeepSeekFileID(raw string) error {
	_, err := validateDeepSeekFileID(raw)
	return err
}

// ensureAnthropicBetaToken appends a required beta capability while retaining
// all client/account-provided tokens. Header names are normalized first so
// mixed-casing input cannot produce duplicate wire headers.
func ensureAnthropicBetaToken(header http.Header, token string) {
	if header == nil {
		return
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	var values []string
	for key, entries := range header {
		if strings.EqualFold(key, "anthropic-beta") {
			values = append(values, entries...)
			delete(header, key)
		}
	}
	parts := make([]string, 0, len(values)+1)
	seen := make(map[string]struct{}, len(values)+1)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			key := strings.ToLower(part)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			parts = append(parts, part)
		}
	}
	if _, exists := seen[strings.ToLower(token)]; !exists {
		parts = append(parts, token)
	}
	header.Set("anthropic-beta", strings.Join(parts, ","))
}

// deepSeekAnthropicRequestUsesFilesAPI detects Anthropic image blocks that use
// a Files API reference. It intentionally ignores unrelated metadata fields
// named file_id; only image.source.file/file_id activates the beta capability.
func deepSeekAnthropicRequestUsesFilesAPI(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return false
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		case map[string]any:
			if typ, _ := node["type"].(string); typ == "image" {
				if source, ok := node["source"].(map[string]any); ok {
					sourceType, _ := source["type"].(string)
					if sourceType == "file" {
						return true
					}
					if fileID, _ := source["file_id"].(string); strings.TrimSpace(fileID) != "" {
						return true
					}
				}
			}
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(root)
}

var (
	errDeepSeekChatImageRole      = errors.New("DeepSeek Chat Completions images are only supported in user messages")
	errDeepSeekResponsesImageRole = errors.New("DeepSeek Responses images are only supported in user or developer messages and structured tool outputs")
	errDeepSeekAnthropicImageRole = errors.New("DeepSeek Messages images are only supported in user messages")
)

// ValidateDeepSeekChatImageRoles rejects media in non-user Chat Completions
// messages. Rejecting the original request avoids silently changing its prompt.
func ValidateDeepSeekChatImageRoles(body []byte) error {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"messages"`)) {
		return nil
	}
	var root struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	for _, message := range root.Messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if deepSeekContentHasMedia(message.Content, deepSeekChatMediaBlock) {
			return errDeepSeekChatImageRole
		}
	}
	return nil
}

func deepSeekChatMediaBlock(raw []byte) bool {
	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		return false
	}
	var typ string
	_ = json.Unmarshal(block["type"], &typ)
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "image", "input_image", "image_url", "file", "output_image":
		return true
	default:
		return false
	}
}

// ValidateDeepSeekAnthropicImageRoles rejects media in the top-level system
// prompt and assistant turns. User tool_result media remains valid.
func ValidateDeepSeekAnthropicImageRoles(body []byte) error {
	if len(body) == 0 ||
		(!bytes.Contains(body, []byte(`"messages"`)) && !bytes.Contains(body, []byte(`"system"`))) {
		return nil
	}
	var root struct {
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	if deepSeekContentHasMedia(root.System, deepSeekAnthropicMediaBlock) {
		return errDeepSeekAnthropicImageRole
	}
	for _, message := range root.Messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			continue
		}
		if deepSeekContentHasMedia(message.Content, deepSeekAnthropicMediaBlock) {
			return errDeepSeekAnthropicImageRole
		}
	}
	return nil
}

func deepSeekAnthropicMediaBlock(raw []byte) bool {
	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		return false
	}
	var typ string
	_ = json.Unmarshal(block["type"], &typ)
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "image", "input_image", "image_url", "file", "output_image":
		return true
	case "tool_result":
		return deepSeekContentHasMedia(block["content"], deepSeekAnthropicMediaBlock)
	default:
		return false
	}
}

// ValidateDeepSeekResponsesImageRoles rejects media in system and assistant
// message items. User/developer messages and structured tool outputs are valid.
func ValidateDeepSeekResponsesImageRoles(body []byte) error {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"input"`)) {
		return nil
	}
	var root struct {
		Input []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	for _, item := range root.Input {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		if role != "system" && role != "assistant" {
			continue
		}
		if deepSeekContentHasMedia(item.Content, deepSeekResponsesMediaBlock) {
			return errDeepSeekResponsesImageRole
		}
	}
	return nil
}

func deepSeekContentHasMedia(raw json.RawMessage, isMedia func([]byte) bool) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	if trimmed[0] == '[' {
		var parts []json.RawMessage
		if err := json.Unmarshal(trimmed, &parts); err != nil {
			return false
		}
		for _, part := range parts {
			if isMedia(part) {
				return true
			}
		}
		return false
	}
	return trimmed[0] == '{' && isMedia(trimmed)
}

func deepSeekResponsesMediaBlock(raw []byte) bool {
	var block map[string]json.RawMessage
	if err := json.Unmarshal(raw, &block); err != nil {
		return false
	}
	var typ string
	_ = json.Unmarshal(block["type"], &typ)
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "image", "input_image", "image_url", "file", "output_image":
		return true
	default:
		return false
	}
}
