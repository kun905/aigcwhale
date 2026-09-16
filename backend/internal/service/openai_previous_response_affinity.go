package service

import "context"

type openAIPreviousResponseAffinityRequiredContextKey struct{}

// WithOpenAIPreviousResponseAffinityRequired requires the first scheduling
// attempt to honor an existing previous_response_id account binding. It is
// used when text/image routing has already confirmed that the continuation
// belongs to the selected pool. Later failover attempts may still migrate when
// the bound account is excluded and the request body can rebuild its context.
func WithOpenAIPreviousResponseAffinityRequired(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIPreviousResponseAffinityRequiredContextKey{}, true)
}

func openAIPreviousResponseAffinityRequired(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	required, _ := ctx.Value(openAIPreviousResponseAffinityRequiredContextKey{}).(bool)
	return required
}
