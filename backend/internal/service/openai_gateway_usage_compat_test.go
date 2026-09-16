//go:build unit

package service

import (
	"context"
	"time"
)

// Preserve the source-group fixtures used by the existing billing unit tests.
func (s *OpenAIGatewayService) calculateOpenAIRecordUsageCost(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	webSearchMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
	longContextBillingGate *bool,
	pricingAt time.Time,
) (*CostBreakdown, error) {
	return s.calculateOpenAIRecordUsageCostWithImagePricing(
		ctx,
		result,
		apiKey,
		openAIImagePricingContext{
			apiKey:               apiKey,
			baseMultiplier:       webSearchMultiplier,
			tokenMultiplier:      multiplier,
			perRequestMultiplier: imageMultiplier,
		},
		billingModels,
		multiplier,
		imageMultiplier,
		videoMultiplier,
		webSearchMultiplier,
		tokens,
		serviceTier,
		longContextBillingGate,
		pricingAt,
	)
}
