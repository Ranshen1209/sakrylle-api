package apicompat

import "strings"

// responsesContentPartFromChatContentPart maps the image/file variants that
// DeepSeek accepts on the Chat Completions endpoint to the Responses
// input_image shape. Text is intentionally handled by the caller.
func responsesContentPartFromChatContentPart(part ChatContentPart) (ResponsesContentPart, bool) {
	filePart := func() (ResponsesContentPart, bool) {
		if fileID := strings.TrimSpace(part.FileID); fileID != "" {
			return ResponsesContentPart{Type: "input_image", FileID: fileID}, true
		}
		fileData := strings.TrimSpace(part.FileData)
		if fileData == "" || isEmptyBase64DataURI(fileData) {
			return ResponsesContentPart{}, false
		}
		// Chat's file_data is a data URL. Responses calls the equivalent field
		// image_url and does not accept file_data/filename on the wire.
		return ResponsesContentPart{Type: "input_image", ImageURL: fileData}, true
	}

	switch part.Type {
	case "image_url", "input_image":
		if part.ImageURL != nil && strings.TrimSpace(part.ImageURL.URL) != "" && !isEmptyBase64DataURI(part.ImageURL.URL) {
			return ResponsesContentPart{
				Type:     "input_image",
				ImageURL: part.ImageURL.URL,
				Detail:   part.ImageURL.Detail,
			}, true
		}
		if strings.TrimSpace(part.FileID) != "" || strings.TrimSpace(part.FileData) != "" {
			result, ok := filePart()
			if ok && part.ImageURL != nil {
				result.Detail = part.ImageURL.Detail
			}
			return result, ok
		}
		return ResponsesContentPart{}, false
	case "file":
		return filePart()
	default:
		// A few clients use input_image directly in an otherwise Chat-shaped
		// content array. Preserve its file fields rather than dropping the part.
		if part.ImageURL != nil || part.FileID != "" || part.FileData != "" {
			if part.FileID != "" || part.FileData != "" {
				result, ok := filePart()
				if ok && part.ImageURL != nil {
					result.Detail = part.ImageURL.Detail
				}
				return result, ok
			}
			return ResponsesContentPart{
				Type:     "input_image",
				ImageURL: imageURLString(part.ImageURL),
				Detail:   imageURLDetail(part.ImageURL),
			}, true
		}
		return ResponsesContentPart{}, false
	}
}

func imageURLString(imageURL *ChatImageURL) string {
	if imageURL == nil {
		return ""
	}
	return imageURL.URL
}

func imageURLDetail(imageURL *ChatImageURL) string {
	if imageURL == nil {
		return ""
	}
	return imageURL.Detail
}

// chatContentPartFromResponsesContentPart maps a Responses input_image to the
// OpenAI Chat content representation while retaining all supported source
// fields. Valid DeepSeek requests contain either image_url or file_id/file_data;
// retaining both on malformed input is preferable to silently discarding one.
func chatContentPartFromResponsesContentPart(part ResponsesContentPart) (ChatContentPart, bool) {
	if strings.TrimSpace(part.ImageURL) != "" {
		return ChatContentPart{
			Type: "image_url",
			ImageURL: &ChatImageURL{
				URL:    part.ImageURL,
				Detail: part.Detail,
			},
			FileID:   part.FileID,
			FileData: part.FileData,
			Filename: part.Filename,
		}, true
	}
	if strings.TrimSpace(part.FileID) != "" || strings.TrimSpace(part.FileData) != "" {
		return ChatContentPart{
			Type:     "file",
			FileID:   part.FileID,
			FileData: part.FileData,
			Filename: part.Filename,
		}, true
	}
	return ChatContentPart{}, false
}

// responsesContentPartFromAnthropicImageSource converts an Anthropic image
// source into a Responses input_image. Anthropic's URL and file source forms
// map directly; base64 data is represented as a data URI in image_url.
func responsesContentPartFromAnthropicImageSource(src *AnthropicImageSource) (ResponsesContentPart, bool) {
	if src == nil {
		return ResponsesContentPart{}, false
	}
	part := ResponsesContentPart{Type: "input_image", Filename: src.Filename}
	if strings.TrimSpace(src.FileID) != "" {
		part.FileID = src.FileID
	} else if strings.TrimSpace(src.FileData) != "" {
		// FileData is a bridge-only compatibility field. DeepSeek's OpenAI
		// shape expects a data URI, while a few clients provide raw base64.
		dataSource := *src
		dataSource.Data = src.FileData
		part.ImageURL = anthropicImageToDataURI(&dataSource)
	} else if strings.TrimSpace(src.URL) != "" {
		part.ImageURL = src.URL
	} else if strings.TrimSpace(src.Data) != "" {
		part.ImageURL = anthropicImageToDataURI(src)
	}
	return part, part.ImageURL != "" || part.FileID != ""
}

// chatContentPartFromAnthropicImageSource converts an Anthropic image source
// directly to Chat Completions content, preserving URL/file forms instead of
// forcing every source through a base64 data URI.
func chatContentPartFromAnthropicImageSource(src *AnthropicImageSource) (ChatContentPart, bool) {
	if src == nil {
		return ChatContentPart{}, false
	}
	if strings.TrimSpace(src.FileID) != "" || strings.TrimSpace(src.FileData) != "" {
		fileData := src.FileData
		if strings.TrimSpace(fileData) != "" {
			dataSource := *src
			dataSource.Data = fileData
			fileData = anthropicImageToDataURI(&dataSource)
		}
		return ChatContentPart{
			Type:     "file",
			FileID:   src.FileID,
			FileData: fileData,
			Filename: src.Filename,
		}, true
	}
	if strings.TrimSpace(src.URL) != "" {
		return ChatContentPart{
			Type:     "image_url",
			ImageURL: &ChatImageURL{URL: src.URL},
		}, true
	}
	if strings.TrimSpace(src.Data) != "" {
		return ChatContentPart{
			Type:     "image_url",
			ImageURL: &ChatImageURL{URL: anthropicImageToDataURI(src)},
		}, true
	}
	return ChatContentPart{}, false
}

// anthropicImageSourceFromResponsesContentPart converts all DeepSeek/OpenAI
// image source forms to Anthropic's source object. Anthropic has no detail
// field; the target API applies its own image handling for URL/file sources.
func anthropicImageSourceFromResponsesContentPart(part ResponsesContentPart) *AnthropicImageSource {
	src := &AnthropicImageSource{Filename: part.Filename}
	if strings.TrimSpace(part.FileID) != "" {
		src.Type = "file"
		src.FileID = part.FileID
		// Keep an accompanying malformed/legacy source visible instead of
		// silently discarding it; valid requests never set both.
		src.URL = part.ImageURL
		src.FileData = part.FileData
		return src
	}
	if strings.TrimSpace(part.FileData) != "" {
		if parsed := dataURIToAnthropicImageSource(part.FileData); parsed != nil {
			parsed.Filename = part.Filename
			return parsed
		}
		src.Type = "base64"
		src.MediaType = "image/png"
		src.Data = part.FileData
		return src
	}
	if strings.TrimSpace(part.ImageURL) != "" {
		if parsed := dataURIToAnthropicImageSource(part.ImageURL); parsed != nil {
			parsed.Filename = part.Filename
			return parsed
		}
		src.Type = "url"
		src.URL = part.ImageURL
		return src
	}
	return nil
}
