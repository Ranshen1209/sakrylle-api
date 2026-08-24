package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelModelPricingResolveAtPeakAndOffPeak(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	staticInput := 2.0
	versionInput := 3.0
	versionOutput := 9.0
	cacheRead := 0.1
	pricing := ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &staticInput,
		TimeVersions: []PricingTimeVersion{{
			ID:                42,
			EffectiveFrom:     time.Date(2026, 8, 17, 0, 0, 0, 0, location),
			Timezone:          "Asia/Shanghai",
			DefaultMultiplier: 0.5,
			InputPrice:        &versionInput,
			OutputPrice:       &versionOutput,
			CacheReadPrice:    &cacheRead,
			Windows: []PricingTimeWindow{
				// DeepSeek's Beijing schedule uses bits 0-4 (Monday-Friday).
				{Label: "peak", Weekdays: 31, StartMinute: 9 * 60, EndMinute: 12 * 60, Multiplier: 1},
				{Label: "peak", Weekdays: 31, StartMinute: 14 * 60, EndMinute: 18 * 60, Multiplier: 1},
			},
		}},
	}

	before, resolution := pricing.ResolveAt(time.Date(2026, 8, 16, 23, 59, 0, 0, location))
	require.Nil(t, resolution)
	require.Equal(t, 2.0, *before.InputPrice)

	tests := []struct {
		name       string
		at         time.Time
		label      string
		multiplier float64
		input      float64
	}{
		{name: "midnight off peak", at: time.Date(2026, 8, 17, 0, 0, 0, 0, location), label: "off_peak", multiplier: 0.5, input: 1.5},
		{name: "just before morning peak", at: time.Date(2026, 8, 17, 8, 59, 0, 0, location), label: "off_peak", multiplier: 0.5, input: 1.5},
		{name: "morning peak starts", at: time.Date(2026, 8, 17, 9, 0, 0, 0, location), label: "peak", multiplier: 1, input: 3},
		{name: "morning peak ends", at: time.Date(2026, 8, 17, 12, 0, 0, 0, location), label: "off_peak", multiplier: 0.5, input: 1.5},
		{name: "afternoon peak starts", at: time.Date(2026, 8, 17, 14, 0, 0, 0, location), label: "peak", multiplier: 1, input: 3},
		{name: "afternoon peak ends", at: time.Date(2026, 8, 17, 18, 0, 0, 0, location), label: "off_peak", multiplier: 0.5, input: 1.5},
		{name: "friday afternoon peak", at: time.Date(2026, 8, 21, 17, 59, 0, 0, location), label: "peak", multiplier: 1, input: 3},
		{name: "saturday is always off peak", at: time.Date(2026, 8, 22, 10, 0, 0, 0, location), label: "off_peak", multiplier: 0.5, input: 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, resolution := pricing.ResolveAt(tt.at)
			require.NotNil(t, resolution)
			require.Equal(t, int64(42), resolution.VersionID)
			require.Equal(t, tt.label, resolution.PeriodLabel)
			require.Equal(t, tt.multiplier, resolution.Multiplier)
			require.Equal(t, tt.input, *resolved.InputPrice)
			require.Equal(t, 2.0, *pricing.InputPrice, "ResolveAt must not mutate the cached price card")
		})
	}
}

func TestValidatePricingTimeVersionsRejectsOverlaps(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	from := time.Date(2026, 8, 17, 0, 0, 0, 0, location)
	until := from.Add(24 * time.Hour)

	err = ValidatePricingTimeVersions([]PricingTimeVersion{
		{
			EffectiveFrom: from, EffectiveUntil: &until, Timezone: "Asia/Shanghai", DefaultMultiplier: 0.5,
			Windows: []PricingTimeWindow{
				{Weekdays: 127, StartMinute: 540, EndMinute: 720, Multiplier: 1},
				{Weekdays: 1, StartMinute: 600, EndMinute: 780, Multiplier: 1},
			},
		},
	}, BillingModeToken, false)
	require.ErrorContains(t, err, "windows 1 and 2 overlap")

	err = ValidatePricingTimeVersions([]PricingTimeVersion{
		{EffectiveFrom: from, EffectiveUntil: &until, Timezone: "Asia/Shanghai", DefaultMultiplier: 0.5},
		{EffectiveFrom: from.Add(12 * time.Hour), Timezone: "Asia/Shanghai", DefaultMultiplier: 0.5},
	}, BillingModeToken, false)
	require.ErrorContains(t, err, "time versions 1 and 2 overlap")

	err = ValidatePricingTimeVersions([]PricingTimeVersion{{
		EffectiveFrom: from, Timezone: "Asia/Shanghai", DefaultMultiplier: 0.5,
	}}, BillingModeToken, true)
	require.ErrorContains(t, err, "cannot be combined")
}
