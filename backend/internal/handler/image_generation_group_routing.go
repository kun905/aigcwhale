package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// errImageGenerationGroupRouting marks a configured server-side route that
// cannot be used. It is intentionally kept separate from user request errors:
// silently falling back to the source group would make an administrator's
// image-routing configuration appear to work while sending traffic elsewhere.
var errImageGenerationGroupRouting = errors.New("image generation target group is unavailable")

// validateImageGenerationGroupTarget validates the one-hop target at request
// time as a defense-in-depth check for stale caches or direct database edits.
// The admin service performs the same checks when a route is configured.
func validateImageGenerationGroupTarget(source, target *service.Group) error {
	if target == nil {
		return fmt.Errorf("%w: group not found", errImageGenerationGroupRouting)
	}
	if target.ID <= 0 {
		return fmt.Errorf("%w: invalid group id", errImageGenerationGroupRouting)
	}
	if source != nil && source.ID > 0 && source.ID == target.ID {
		return fmt.Errorf("%w: source and target groups are the same", errImageGenerationGroupRouting)
	}
	if target.Status != service.StatusActive {
		return fmt.Errorf("%w: target group is not active", errImageGenerationGroupRouting)
	}
	if target.Platform != service.PlatformOpenAI {
		return fmt.Errorf("%w: target group platform is not openai", errImageGenerationGroupRouting)
	}
	if !target.AllowImageGeneration {
		return fmt.Errorf("%w: target group does not allow image generation", errImageGenerationGroupRouting)
	}
	// A target configured with another target would make runtime behavior depend
	// on whether the request entered through this handler more than once. Admin
	// writes reject this shape; reject stale/manual data here as well.
	if target.ImageGenerationGroupID != nil {
		return fmt.Errorf("%w: target group has another image-generation target", errImageGenerationGroupRouting)
	}
	return nil
}

// routeImageGenerationGroup records the configured image-generation target on
// the request context. It deliberately does not replace the authenticated
// APIKey or ctxkey.Group: the source group must continue to own authorization,
// quota, RPM, subscription checks, and usage-log attribution.
//
// It returns routed=false when no route is configured, preserving the
// pre-existing request path exactly in that case.
func (h *OpenAIGatewayHandler) routeImageGenerationGroup(c *gin.Context) (routed bool, err error) {
	if c == nil || c.Request == nil {
		return false, nil
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || apiKey.Group.ImageGenerationGroupID == nil {
		return false, nil
	}

	source := apiKey.Group
	targetID := *source.ImageGenerationGroupID
	if targetID <= 0 {
		return false, fmt.Errorf("%w: configured target group id is invalid", errImageGenerationGroupRouting)
	}
	if source.Platform != service.PlatformOpenAI {
		return false, fmt.Errorf("%w: image-generation routing is only supported for openai source groups", errImageGenerationGroupRouting)
	}
	if h == nil || h.gatewayService == nil {
		return false, fmt.Errorf("%w: gateway service is unavailable", errImageGenerationGroupRouting)
	}

	target, resolveErr := h.gatewayService.ResolveGroupByID(c.Request.Context(), targetID)
	if resolveErr != nil {
		return false, fmt.Errorf("%w: resolve target group: %v", errImageGenerationGroupRouting, resolveErr)
	}
	if validateErr := validateImageGenerationGroupTarget(source, target); validateErr != nil {
		return false, validateErr
	}

	installImageGenerationGroupContext(c, target)
	return true, nil
}

// installImageGenerationGroupContext stores the target independently from the
// authenticated source API key. Scheduling callers can read the target via
// service.OpenAIImageGenerationGroupFromContext.
func installImageGenerationGroupContext(c *gin.Context, target *service.Group) {
	if c == nil || c.Request == nil || target == nil {
		return
	}
	c.Request = c.Request.WithContext(
		service.WithOpenAIImageGenerationGroup(c.Request.Context(), target),
	)
}

// imageGenerationRoutingGroupID returns the group used for image account
// selection, channel mapping, and sticky-session state. The authenticated
// source group remains the owner of authorization, quota, and usage billing.
func imageGenerationRoutingGroupID(ctx context.Context, sourceGroupID *int64) *int64 {
	if targetID, ok := service.OpenAIImageGenerationGroupIDFromContext(ctx); ok {
		return targetID
	}
	return sourceGroupID
}

// alternateImageGenerationRoutingGroupID returns the other account-pool group
// that the same source API key can use when switching between text and image
// mode. It is used only to detect an existing previous_response_id binding in
// the other pool; authorization ownership remains keyed to the source group.
func alternateImageGenerationRoutingGroupID(apiKey *service.APIKey, imageIntent bool, routingGroupID *int64) *int64 {
	if apiKey == nil || apiKey.GroupID == nil || *apiKey.GroupID <= 0 {
		return nil
	}
	currentID := int64(0)
	if routingGroupID != nil {
		currentID = *routingGroupID
	}
	if imageIntent {
		if *apiKey.GroupID != currentID {
			return apiKey.GroupID
		}
		return nil
	}
	if apiKey.Group == nil || apiKey.Group.ImageGenerationGroupID == nil || *apiKey.Group.ImageGenerationGroupID <= 0 {
		return nil
	}
	targetID := *apiKey.Group.ImageGenerationGroupID
	if targetID == currentID {
		return nil
	}
	return &targetID
}

// imageGenerationPermissionGroup selects the group whose image capability is
// being used. A configured target must opt in to image generation even when
// the source GPT group itself has image generation disabled.
func imageGenerationPermissionGroup(ctx context.Context, source *service.Group) *service.Group {
	if target, ok := service.OpenAIImageGenerationGroupFromContext(ctx); ok {
		return target
	}
	return source
}

// imageGenerationModelCandidates returns every model value that a downstream
// parser could bind, plus the effective model chosen by the Images parser.
// Including both prevents duplicate/case-variant JSON keys or repeated
// multipart fields from bypassing the routed target group's allowlist.
func imageGenerationModelCandidates(path, contentType string, body []byte, effectiveModel string) []string {
	candidates := requestmodel.FromBodyCandidates(path, contentType, body)
	if effectiveModel = strings.TrimSpace(effectiveModel); effectiveModel != "" {
		candidates = append([]string{effectiveModel}, candidates...)
	}
	return candidates
}

// blockedImageGenerationGroupModel checks only the routed target group. The
// authenticated source group remains covered by GroupModelAllowlist (or the WS
// frame checks) and must not be substituted here.
func blockedImageGenerationGroupModel(ctx context.Context, candidates []string) string {
	target, routed := service.OpenAIImageGenerationGroupFromContext(ctx)
	if !routed || target == nil || !target.ModelAllowlistEnabled() {
		return ""
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && !target.ModelAllowlist.Allows(candidate) {
			return candidate
		}
	}
	return ""
}

// imageGenerationWebSocketTurnAllowed keeps the account pool fixed for the
// lifetime of a Responses WebSocket. Switching between text and image routing
// would require releasing the current upstream connection and selecting from a
// different group, which the existing WS relay cannot do between turns.
func imageGenerationWebSocketTurnAllowed(sessionImageIntent, turnImageIntent bool) bool {
	return sessionImageIntent == turnImageIntent
}
