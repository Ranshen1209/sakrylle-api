package service

import (
	"context"
	"time"
)

type pricingAtContextKey struct{}

// withPricingAt freezes the channel price-card selection for the lifetime of a request.
func withPricingAt(ctx context.Context, at time.Time) context.Context {
	if at.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, pricingAtContextKey{}, at)
}

func pricingAtFromContext(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	at, _ := ctx.Value(pricingAtContextKey{}).(time.Time)
	return at
}
