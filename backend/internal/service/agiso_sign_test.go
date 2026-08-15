package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgisoPushSign_MatchesVerifiedFormula(t *testing.T) {
	jsonValue := `{"biz_order_id":1234567890,"item_id":98765,"order_status":2,"seller_id":54321}`
	sign := AgisoPushSign("secret", jsonValue, "1710000000")

	require.Equal(t, "f99389c8d88e0482dbc939a1df34d885", sign)
	require.True(t, AgisoPushSignValid("secret", jsonValue, "1710000000", strings.ToUpper(sign)))
}

func TestAgisoAPISign_SortsASCIIAndExcludesSign(t *testing.T) {
	params := map[string]string{
		"modifyTimeStart": "2016-07-13 10:44:30",
		"pageNo":          "1",
		"pageSize":        "20",
		"timestamp":       "1468476350",
		"sign":            "ignore-me",
	}

	// Agiso's docs publish this exact parameter set and拼接顺序, but redact
	// AppSecret before showing the final hash. This vector uses the same rule
	// with a non-redacted test secret.
	require.Equal(t, "fdffe6d059eaa9cf267dbc3bdd7d9cd4", AgisoAPISign("secret", params))
}

func TestAgisoDeliveryEvent_TargetPaymentAndRefund(t *testing.T) {
	payment, err := ParseAgisoDeliveryEvent("AldsIdle", "1", `{"biz_order_id":123,"item_id":456,"order_status":2,"seller_id":789}`)
	require.NoError(t, err)
	require.True(t, payment.IsPayment())
	require.False(t, payment.IsRefund())
	require.Equal(t, "123", payment.BizOrderID)
	require.Equal(t, "456", payment.ItemID)
	require.Equal(t, "789", payment.SellerID)

	ignored, err := ParseAgisoDeliveryEvent("AldsIdle", "1", `{"biz_order_id":123,"order_status":3}`)
	require.NoError(t, err)
	require.False(t, ignored.IsPayment())

	refund, err := ParseAgisoDeliveryEvent("AldsIdle", "8", `{"biz_order_id":123,"order_status":5}`)
	require.NoError(t, err)
	require.True(t, refund.IsRefund())
}

func TestAgisoRedeemValue_UsesPaymentCentsWithoutFXConversion(t *testing.T) {
	value, err := AgisoRedeemValue(1099, 1.5)
	require.NoError(t, err)
	require.Equal(t, 16.485, value)

	_, err = AgisoRedeemValue(0, 1)
	require.Error(t, err)
}

func TestAgisoDeliveryMessage_UnderAgisoLimit(t *testing.T) {
	msg, err := BuildAgisoDeliveryMessage(12.34, "ABCD-EFGH-IJKL-MNOP")
	require.NoError(t, err)
	require.Contains(t, msg, "https://sub.sakrylle.com/redeem")
	require.Contains(t, msg, "ABCD-EFGH-IJKL-MNOP")
	require.LessOrEqual(t, len([]rune(msg)), 1000)
}
