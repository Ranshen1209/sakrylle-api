package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAgisoClient_OrderDetailSignsAndParsesResponse(t *testing.T) {
	var gotAuth, gotVersion, gotSign string
	client := newTestAgisoClient(func(r *http.Request) *http.Response {
		require.Equal(t, "/Order/Detail", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		require.NoError(t, r.ParseForm())
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("ApiVersion")
		gotSign = r.Form.Get("sign")
		require.Equal(t, "1234567890", r.Form.Get("tid"))
		require.NotEmpty(t, r.Form.Get("timestamp"))
		expected := AgisoAPISign("secret", map[string]string{"tid": "1234567890", "timestamp": r.Form.Get("timestamp")})
		require.Equal(t, expected, gotSign)
		return jsonResponse(`{"IsSuccess":true,"Error_Code":0,"Error_Msg":"","AllowRetry":false,"RequestId":"req-1","Data":{"payment":1099,"encryption_buyer_id":"buyer-1","order_status":2}}`)
	})
	detail, err := client.OrderDetail(context.Background(), "1234567890")
	require.NoError(t, err)
	require.Equal(t, "Bearer token", gotAuth)
	require.Equal(t, "1", gotVersion)
	require.Equal(t, int64(1099), detail.PaymentCents)
	require.Equal(t, "buyer-1", detail.BuyerID)
}

func TestAgisoClient_SendMsgAndDummySendUseExpectedPaths(t *testing.T) {
	paths := []string{}
	client := newTestAgisoClient(func(r *http.Request) *http.Response {
		paths = append(paths, r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		require.NoError(t, r.ParseForm())
		require.Equal(t, "1001", r.Form.Get("tid"))
		if r.URL.Path == "/ImMsg/SendMsg" {
			require.Equal(t, "hello", r.Form.Get("msg"))
		}
		return jsonResponse(`{"IsSuccess":true,"Error_Code":0,"Error_Msg":"","AllowRetry":false,"RequestId":"req-1","Data":{}}`)
	})
	require.NoError(t, client.SendMsg(context.Background(), "1001", "hello"))
	require.NoError(t, client.DummySend(context.Background(), "1001"))
	require.Equal(t, []string{"/ImMsg/SendMsg", "/Order/DummySend"}, paths)
}

func TestAgisoClient_ReturnsRetryableErrorFromEnvelope(t *testing.T) {
	client := newTestAgisoClient(func(r *http.Request) *http.Response {
		return jsonResponse(`{"IsSuccess":false,"Error_Code":2,"Error_Msg":"limit","AllowRetry":true,"RequestId":"req-2"}`)
	})
	_, err := client.OrderDetail(context.Background(), "1001")
	require.Error(t, err)
	var agisoErr *AgisoAPIError
	require.ErrorAs(t, err, &agisoErr)
	require.True(t, agisoErr.AllowRetry)
	require.Equal(t, 2, agisoErr.Code)
}

func newTestAgisoClient(fn func(*http.Request) *http.Response) *AgisoClient {
	limiter := make(chan time.Time, 10)
	for i := 0; i < cap(limiter); i++ {
		limiter <- time.Now()
	}
	return &AgisoClient{
		cfg:        config.AgisoConfig{AppSecret: "secret", AccessToken: "token", APIBase: "https://agiso.test"},
		httpClient: &http.Client{Transport: agisoRoundTripFunc(fn)},
		limiter:    limiter,
	}
}

type agisoRoundTripFunc func(*http.Request) *http.Response

func (f agisoRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r), nil
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
