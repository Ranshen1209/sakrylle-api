package service

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeGrokImageResponseDefaultsToBase64JSON(t *testing.T) {
	raw := []byte(`{"data":[{"url":"data:image/png;base64,AQID"}]}`)

	got, err := (&OpenAIGatewayService{}).normalizeGrokImageResponse(
		context.Background(),
		GrokMediaEndpointImagesGenerations,
		nil,
		raw,
		"",
	)

	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{1, 2, 3}), gjson.GetBytes(got, "data.0.b64_json").String())
	require.False(t, gjson.GetBytes(got, "data.0.url").Exists())
}

func TestNormalizeGrokImageResponseHonorsURLFormat(t *testing.T) {
	raw := []byte(`{"data":[{"b64_json":"AQID"}]}`)

	got, err := (&OpenAIGatewayService{}).normalizeGrokImageResponse(
		context.Background(),
		GrokMediaEndpointImagesEdits,
		nil,
		raw,
		"url",
	)

	require.NoError(t, err)
	require.Equal(t, "data:image/png;base64,AQID", gjson.GetBytes(got, "data.0.url").String())
	require.False(t, gjson.GetBytes(got, "data.0.b64_json").Exists())
}
