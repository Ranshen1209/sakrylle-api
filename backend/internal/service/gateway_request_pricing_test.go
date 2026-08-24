//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithGatewayTokenRequestPricingMarksOnlyExplicitTokenRequests(t *testing.T) {
	ctx, pricingAt := WithGatewayTokenRequestPricing(context.Background())

	got, ok := gatewayTokenRequestPricingAtFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, pricingAt, got)
	require.Equal(t, pricingAt, GatewayTokenRequestPricingAtFromContext(ctx))
	require.True(t, GatewayTokenRequestPricingAtFromContext(context.Background()).IsZero())
}

func TestWithGatewayTokenRequestPricingAtKeepsIngressTimestamp(t *testing.T) {
	ingress := time.Date(2026, 8, 17, 0, 59, 59, 0, time.UTC)
	ctx, pricingAt := WithGatewayTokenRequestPricingAt(context.Background(), ingress)

	require.Equal(t, ingress, pricingAt)
	require.Equal(t, ingress, GatewayTokenRequestPricingAtFromContext(ctx))
}
