package handler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesImageRoutingUsesRoutedProfitPricingGroup(t *testing.T) {
	source := goFunctionSource(t, "openai_gateway_handler.go", "Responses")
	require.Contains(t, source, "pricingGroupID := apiKey.GroupID")
	require.Contains(t, source, "pricingGroupID = routingGroupID")
	require.Contains(t, source, "WithOpenAIRequestPricingContext(c.Request.Context(), pricingGroupID)")
	require.Less(t,
		strings.Index(source, "pricingGroupID = routingGroupID"),
		strings.Index(source, "WithOpenAIRequestPricingContext(c.Request.Context(), pricingGroupID)"),
	)
}

func TestResponsesWebSocketImageRoutingUsesRoutedProfitPricingGroup(t *testing.T) {
	source := goFunctionSource(t, "openai_gateway_handler.go", "ResponsesWebSocket")
	require.Contains(t, source, "wsPricingGroupID := apiKey.GroupID")
	require.Contains(t, source, "wsPricingGroupID = routingGroupID")
	require.Contains(t, source, "WithOpenAIRequestPricingContext(ctx, wsPricingGroupID)")
	require.Contains(t, source, "WithOpenAITurnPricingContext(ctx, wsPricingGroupID)")
}

func TestResponsesImageRoutingDiagnosesActualTargetPool(t *testing.T) {
	source := goFunctionSource(t, "openai_gateway_handler.go", "Responses")
	require.Equal(t, 2, strings.Count(source,
		"classifyNoAccountErrorForGroupFromGin(c, h.gatewayService, routingGroupID,"))
}

func TestImagesRoutingDiagnosesActualTargetPool(t *testing.T) {
	source := goFunctionSource(t, "openai_images.go", "Images")
	require.Equal(t, 2, strings.Count(source,
		"classifyNoAccountErrorForGroupFromGin(c, h.gatewayService, routingGroupID,"))
}
