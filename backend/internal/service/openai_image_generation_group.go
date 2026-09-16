package service

import "context"

// openAIImageGenerationGroupContextKey carries the account-pool/pricing group
// selected for an explicit OpenAI image request. It is deliberately separate
// from ctxkey.Group: the latter is the authenticated source group and is used
// by quota, subscription, RPM, and other request-level authorization paths.
//
// Keeping the target in its own context value lets image routing share the
// existing scheduler APIs (which accept an explicit group ID) without
// replacing the authenticated API key or changing billing ownership.
type openAIImageGenerationGroupContextKey struct{}

// WithOpenAIImageGenerationGroup records the target group for an explicit
// image-generation request. A nil group clears the target marker.
func WithOpenAIImageGenerationGroup(ctx context.Context, group *Group) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if group == nil {
		return context.WithValue(ctx, openAIImageGenerationGroupContextKey{}, (*Group)(nil))
	}
	return context.WithValue(ctx, openAIImageGenerationGroupContextKey{}, group)
}

// OpenAIImageGenerationGroupFromContext returns the explicitly selected image
// target, if request-level image routing has been enabled for this request.
func OpenAIImageGenerationGroupFromContext(ctx context.Context) (*Group, bool) {
	if ctx == nil {
		return nil, false
	}
	group, ok := ctx.Value(openAIImageGenerationGroupContextKey{}).(*Group)
	return group, ok && IsGroupContextValid(group)
}

// OpenAIImageGenerationGroupIDFromContext returns the target ID used by the
// account scheduler and channel mapping. Callers should keep the authenticated
// API key's GroupID for quota, rate limits, and usage-log ownership.
func OpenAIImageGenerationGroupIDFromContext(ctx context.Context) (*int64, bool) {
	group, ok := OpenAIImageGenerationGroupFromContext(ctx)
	if !ok || group.ID <= 0 {
		return nil, false
	}
	id := group.ID
	return &id, true
}

// OpenAIImageGenerationGroupIDForRequest returns the account-pool group ID for
// an explicit image request, falling back to the authenticated source group
// when no image route is configured. The fallback keeps every existing caller
// on its original scheduling path.
func OpenAIImageGenerationGroupIDForRequest(ctx context.Context, sourceID *int64) *int64 {
	if targetID, ok := OpenAIImageGenerationGroupIDFromContext(ctx); ok {
		return targetID
	}
	return sourceID
}

// OpenAIImageGenerationGroupForRequest returns the group whose image-specific
// capability and pricing should be used for this request. It never changes
// the authenticated API key's source group.
func OpenAIImageGenerationGroupForRequest(ctx context.Context, source *Group) *Group {
	if target, ok := OpenAIImageGenerationGroupFromContext(ctx); ok {
		return target
	}
	return source
}
