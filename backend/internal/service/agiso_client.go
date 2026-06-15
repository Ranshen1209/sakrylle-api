package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type AgisoClient struct {
	cfg        config.AgisoConfig
	httpClient *http.Client
	limiter    <-chan time.Time
}

type AgisoAPIError struct {
	Code       int
	Message    string
	AllowRetry bool
	RequestID  string
}

func (e *AgisoAPIError) Error() string {
	return fmt.Sprintf("agiso api error code=%d allow_retry=%t request_id=%s: %s", e.Code, e.AllowRetry, e.RequestID, e.Message)
}

type agisoEnvelope struct {
	IsSuccess  bool            `json:"IsSuccess"`
	ErrorCode  int             `json:"Error_Code"`
	ErrorMsg   string          `json:"Error_Msg"`
	AllowRetry bool            `json:"AllowRetry"`
	RequestID  string          `json:"RequestId"`
	Data       json.RawMessage `json:"Data"`
}

func NewAgisoClient(cfg config.AgisoConfig) *AgisoClient {
	return &AgisoClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		limiter:    time.Tick(50 * time.Millisecond), // Agiso docs: 20 QPS.
	}
}

func (c *AgisoClient) OrderDetail(ctx context.Context, tid string) (AgisoOrderDetail, error) {
	data, err := c.post(ctx, "/Order/Detail", map[string]string{"tid": tid})
	if err != nil {
		return AgisoOrderDetail{}, err
	}
	var payload struct {
		Payment           int64  `json:"payment"`
		BuyerID           string `json:"buyer_id"`
		EncryptionBuyerID string `json:"encryption_buyer_id"`
		OrderStatus       int    `json:"order_status"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			return AgisoOrderDetail{}, err
		}
	}
	buyerID := payload.EncryptionBuyerID
	if buyerID == "" {
		buyerID = payload.BuyerID
	}
	return AgisoOrderDetail{PaymentCents: payload.Payment, BuyerID: buyerID, OrderStatus: payload.OrderStatus, RawJSON: string(data)}, nil
}

func (c *AgisoClient) SendMsg(ctx context.Context, tid, msg string) error {
	if len([]rune(msg)) > AgisoMaxMsgRunes {
		return fmt.Errorf("agiso message exceeds %d characters", AgisoMaxMsgRunes)
	}
	_, err := c.post(ctx, "/ImMsg/SendMsg", map[string]string{"tid": tid, "msg": msg})
	return err
}

func (c *AgisoClient) DummySend(ctx context.Context, tid string) error {
	_, err := c.post(ctx, "/Order/DummySend", map[string]string{"tid": tid})
	return err
}

func (c *AgisoClient) post(ctx context.Context, path string, params map[string]string) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.limiter:
	}

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	form.Set("timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	signParams := make(map[string]string, len(form))
	for k, values := range form {
		if len(values) > 0 {
			signParams[k] = values[0]
		}
	}
	form.Set("sign", AgisoAPISign(c.cfg.AppSecret, signParams))

	base := strings.TrimRight(c.cfg.APIBase, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	req.Header.Set("ApiVersion", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("agiso http status %d", resp.StatusCode)
	}
	var env agisoEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if !env.IsSuccess {
		return nil, &AgisoAPIError{Code: env.ErrorCode, Message: env.ErrorMsg, AllowRetry: env.AllowRetry, RequestID: env.RequestID}
	}
	return env.Data, nil
}
