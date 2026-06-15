package service

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	AgisoPlatformAldsIdle = "AldsIdle"
	AgisoAopicTradePay    = "1"
	AgisoAopicRefund      = "8"
	AgisoTradeStatusPaid  = 2
	AgisoRedeemURL        = "https://sub.sakrylle.com/redeem"
	AgisoMaxMsgRunes      = 1000
)

type AgisoDeliveryEvent struct {
	FromPlatform string
	Aopic        string
	BizOrderID   string
	ItemID       string
	OrderStatus  int
	SellerID     string
	RawJSON      string
}

func (e AgisoDeliveryEvent) IsPayment() bool {
	return e.FromPlatform == AgisoPlatformAldsIdle && e.Aopic == AgisoAopicTradePay && e.OrderStatus == AgisoTradeStatusPaid && e.BizOrderID != ""
}

func (e AgisoDeliveryEvent) IsRefund() bool {
	return e.FromPlatform == AgisoPlatformAldsIdle && e.Aopic == AgisoAopicRefund && e.BizOrderID != ""
}

func AgisoPushSign(appSecret, jsonValue, timestamp string) string {
	return agisoMD5(appSecret + "json" + jsonValue + "timestamp" + timestamp + appSecret)
}

func AgisoPushSignValid(appSecret, jsonValue, timestamp, got string) bool {
	return strings.EqualFold(AgisoPushSign(appSecret, jsonValue, timestamp), strings.TrimSpace(got))
}

func AgisoAPISign(appSecret string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "byte[]" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	_, _ = b.WriteString(appSecret)
	for _, k := range keys {
		_, _ = b.WriteString(k)
		_, _ = b.WriteString(params[k])
	}
	_, _ = b.WriteString(appSecret)
	return agisoMD5(b.String())
}

func ParseAgisoDeliveryEvent(fromPlatform, aopic, rawJSON string) (AgisoDeliveryEvent, error) {
	dec := json.NewDecoder(strings.NewReader(rawJSON))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		return AgisoDeliveryEvent{}, err
	}

	status, _ := intFromJSONValue(payload["order_status"])
	return AgisoDeliveryEvent{
		FromPlatform: strings.TrimSpace(fromPlatform),
		Aopic:        strings.TrimSpace(aopic),
		BizOrderID:   stringFromJSONValue(payload["biz_order_id"]),
		ItemID:       stringFromJSONValue(payload["item_id"]),
		OrderStatus:  status,
		SellerID:     stringFromJSONValue(payload["seller_id"]),
		RawJSON:      rawJSON,
	}, nil
}

func AgisoRedeemValue(paymentCents int64, multiplier float64) (float64, error) {
	if paymentCents <= 0 {
		return 0, errors.New("payment_cents must be greater than zero")
	}
	if multiplier <= 0 {
		return 0, errors.New("value multiplier must be greater than zero")
	}
	return (float64(paymentCents) / 100) * multiplier, nil
}

func BuildAgisoDeliveryMessage(value float64, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", errors.New("code is required")
	}
	msg := fmt.Sprintf("【Sakrylle 充值码】面值 ￥%.2f\n兑换地址:%s\n兑换码:%s\n步骤:登录账号 → 打开兑换地址 → 粘贴兑换码 → 余额到账。\n如未到账请联系客服,勿在确认收货前删除本消息。", value, AgisoRedeemURL, code)
	if len([]rune(msg)) > AgisoMaxMsgRunes {
		return "", errors.New("agiso message exceeds 1000 characters")
	}
	return msg, nil
}

func agisoMD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func stringFromJSONValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatInt(int64(x), 10)
	default:
		return ""
	}
}

func intFromJSONValue(v any) (int, bool) {
	switch x := v.(type) {
	case json.Number:
		i, err := x.Int64()
		if err == nil {
			return int(i), true
		}
	case float64:
		return int(x), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(x))
		if err == nil {
			return i, true
		}
	}
	return 0, false
}
