package handler

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestValidateImageGenerationGroupTarget(t *testing.T) {
	source := &service.Group{ID: 10, Platform: service.PlatformOpenAI}
	valid := &service.Group{
		ID:                   20,
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		Hydrated:             true,
		AllowImageGeneration: true,
	}

	tests := []struct {
		name    string
		target  *service.Group
		wantErr string
	}{
		{name: "valid", target: valid},
		{name: "missing", target: nil, wantErr: "group not found"},
		{name: "self", target: &service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true}, wantErr: "same"},
		{name: "inactive", target: &service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusDisabled, AllowImageGeneration: true}, wantErr: "not active"},
		{name: "wrong platform", target: &service.Group{ID: 20, Platform: service.PlatformAnthropic, Status: service.StatusActive, AllowImageGeneration: true}, wantErr: "not openai"},
		{name: "image disabled", target: &service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusActive}, wantErr: "does not allow"},
		{name: "chained", target: func() *service.Group {
			id := int64(30)
			return &service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true, ImageGenerationGroupID: &id}
		}(), wantErr: "another"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImageGenerationGroupTarget(source, tt.target)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestRouteImageGenerationGroupNoConfigurationPreservesRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	group := &service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	key := &service.APIKey{ID: 7, GroupID: &group.ID, Group: group}
	c.Set(string(middleware2.ContextKeyAPIKey), key)

	routed, err := (&OpenAIGatewayHandler{}).routeImageGenerationGroup(c)
	require.NoError(t, err)
	require.False(t, routed)
	got, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	require.Same(t, key, got)
	_, contextGroupSet := service.OpenAIImageGenerationGroupFromContext(c.Request.Context())
	require.False(t, contextGroupSet)
}

func TestInstallImageGenerationGroupContextKeepsSourceAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	source := &service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive}
	target := &service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, AllowImageGeneration: true}
	key := &service.APIKey{ID: 7, UserID: 9, GroupID: &source.ID, Group: source}
	c.Set(string(middleware2.ContextKeyAPIKey), key)

	installImageGenerationGroupContext(c, target)

	got, ok := middleware2.GetAPIKeyFromContext(c)
	require.True(t, ok)
	require.Same(t, key, got)
	require.Equal(t, int64(10), *got.GroupID)
	require.Same(t, source, got.Group)
	targetFromContext, targetOK := service.OpenAIImageGenerationGroupFromContext(c.Request.Context())
	require.True(t, targetOK)
	require.Same(t, target, targetFromContext)
	// The source platform remains the quota platform; routing must not move
	// API-key quota/rate-limit accounting to the target group.
	require.Equal(t, service.PlatformOpenAI, service.QuotaPlatform(c.Request.Context(), got))
	require.Same(t, source, key.Group)
	require.Equal(t, int64(10), *key.GroupID)
}

func TestBlockedImageGenerationGroupModelUsesOnlyRoutedTarget(t *testing.T) {
	target := &service.Group{
		ID:       20,
		Hydrated: true,
		Platform: service.PlatformOpenAI,
		Status:   service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{
			Enabled: true,
			Models:  []string{"gpt-image-*"},
		},
	}
	ctx := service.WithOpenAIImageGenerationGroup(context.Background(), target)

	require.Empty(t, blockedImageGenerationGroupModel(ctx, []string{"gpt-image-1"}))
	require.Equal(t, "dall-e-3", blockedImageGenerationGroupModel(ctx, []string{"gpt-image-1", "dall-e-3"}))
	// A source-group allowlist must not be consulted when no routed target is
	// installed; the normal middleware owns that check.
	require.Empty(t, blockedImageGenerationGroupModel(context.Background(), []string{"dall-e-3"}))
}

func TestImageGenerationModelCandidatesCoverEffectiveAndDuplicateJSONModels(t *testing.T) {
	candidates := imageGenerationModelCandidates(
		"/v1/images/generations",
		"application/json",
		[]byte(`{"model":"gpt-image-1","Model":"dall-e-3"}`),
		"gpt-image-default",
	)
	target := &service.Group{
		ID:       20,
		Hydrated: true,
		Platform: service.PlatformOpenAI,
		Status:   service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{
			Enabled: true,
			Models:  []string{"gpt-image-*"},
		},
	}
	ctx := service.WithOpenAIImageGenerationGroup(context.Background(), target)

	require.Equal(t, "dall-e-3", blockedImageGenerationGroupModel(ctx, candidates))
}

func TestImageGenerationModelCandidatesCoverRepeatedMultipartModels(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	require.NoError(t, writer.WriteField("model", "dall-e-3"))
	require.NoError(t, writer.Close())

	candidates := imageGenerationModelCandidates(
		"/v1/images/edits",
		writer.FormDataContentType(),
		body.Bytes(),
		"dall-e-3",
	)
	target := &service.Group{
		ID:       20,
		Hydrated: true,
		Platform: service.PlatformOpenAI,
		Status:   service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{
			Enabled: true,
			Models:  []string{"gpt-image-*"},
		},
	}
	ctx := service.WithOpenAIImageGenerationGroup(context.Background(), target)

	require.Equal(t, "dall-e-3", blockedImageGenerationGroupModel(ctx, candidates))
}

func TestResponsesImageGenerationTargetAllowlistHTTPAndWebSocketCandidates(t *testing.T) {
	target := &service.Group{
		ID:       20,
		Hydrated: true,
		Platform: service.PlatformOpenAI,
		Status:   service.StatusActive,
		ModelAllowlist: service.GroupModelAllowlist{
			Enabled: true,
			Models:  []string{"gpt-5.4"},
		},
	}
	ctx := service.WithOpenAIImageGenerationGroup(context.Background(), target)

	t.Run("http responses", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","Model":"gpt-4.1","tools":[{"type":"image_generation"}]}`)
		candidates := imageGenerationModelCandidates("", "application/json", body, "gpt-5.4")
		require.Equal(t, "gpt-4.1", blockedImageGenerationGroupModel(ctx, candidates))
	})

	t.Run("websocket first frame", func(t *testing.T) {
		frame := []byte(`{"type":"response.create","model":"gpt-5.4"}`)
		candidates := imageGenerationModelCandidates("", "application/json", frame, "gpt-5.4")
		require.Empty(t, blockedImageGenerationGroupModel(ctx, candidates))
	})

	t.Run("websocket later turn", func(t *testing.T) {
		// BeforeRequest prepends the effective model and appends every frame-level
		// candidate. A disallowed model switch must fail closed on an image route.
		candidates := []string{"gpt-4.1", "gpt-4.1"}
		require.Equal(t, "gpt-4.1", blockedImageGenerationGroupModel(ctx, candidates))
	})
}

func TestImageGenerationAffinityGroupIDUsesTargetWhileOwnershipStaysOnSource(t *testing.T) {
	sourceID := int64(10)
	target := &service.Group{
		ID:       20,
		Hydrated: true,
		Platform: service.PlatformOpenAI,
		Status:   service.StatusActive,
	}
	ctx := service.WithOpenAIImageGenerationGroup(context.Background(), target)

	routingID := imageGenerationRoutingGroupID(ctx, &sourceID)
	require.NotNil(t, routingID)
	require.Equal(t, target.ID, *routingID)
	// The source ID is still available to quota/usage callers through the API
	// key; the affinity helper does not mutate it.
	require.Equal(t, int64(10), sourceID)
}

func TestAlternateImageGenerationRoutingGroupID(t *testing.T) {
	sourceID := int64(10)
	targetID := int64(20)
	apiKey := &service.APIKey{
		GroupID: &sourceID,
		Group:   &service.Group{ID: sourceID, ImageGenerationGroupID: &targetID},
	}

	require.Equal(t, targetID, *alternateImageGenerationRoutingGroupID(apiKey, false, &sourceID))
	require.Equal(t, sourceID, *alternateImageGenerationRoutingGroupID(apiKey, true, &targetID))
	require.Nil(t, alternateImageGenerationRoutingGroupID(apiKey, true, &sourceID))

	apiKey.Group.ImageGenerationGroupID = nil
	require.Nil(t, alternateImageGenerationRoutingGroupID(apiKey, false, &sourceID))
}

func TestImageGenerationWebSocketTurnAllowedRequiresStableRoutingMode(t *testing.T) {
	require.True(t, imageGenerationWebSocketTurnAllowed(false, false))
	require.True(t, imageGenerationWebSocketTurnAllowed(true, true))
	require.False(t, imageGenerationWebSocketTurnAllowed(false, true))
	require.False(t, imageGenerationWebSocketTurnAllowed(true, false))
}

type imageRoutingAvailabilityDiagnoser struct {
	byGroup map[int64]service.ModelAvailabilityDiagnosis
	calls   []int64
}

func (d *imageRoutingAvailabilityDiagnoser) DiagnoseModelAvailabilityForPlatform(
	_ context.Context,
	groupID *int64,
	_, _ string,
) service.ModelAvailabilityDiagnosis {
	if groupID == nil {
		return service.ModelAvailabilityDiagnosis{}
	}
	d.calls = append(d.calls, *groupID)
	return d.byGroup[*groupID]
}

func TestClassifyNoAccountErrorForRoutedImageGroupUsesTargetPool(t *testing.T) {
	sourceID := int64(10)
	targetID := int64(20)

	t.Run("target pool model mismatch returns 404 even when source would return 503", func(t *testing.T) {
		diag := &imageRoutingAvailabilityDiagnoser{byGroup: map[int64]service.ModelAvailabilityDiagnosis{
			sourceID: {HasAccountsInPool: false, HasModelSupport: false},
			targetID: {HasAccountsInPool: true, HasModelSupport: false},
		}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

		cls := classifyNoAccountErrorForGroupFromGin(
			c, diag, &targetID, "gpt-image-2", "gpt-image-2", service.PlatformOpenAI,
		)

		require.Equal(t, http.StatusNotFound, cls.Status)
		require.True(t, cls.ModelNotFound)
		require.Equal(t, []int64{targetID}, diag.calls)
	})

	t.Run("target pool temporary exhaustion returns 503 even when source would return 404", func(t *testing.T) {
		diag := &imageRoutingAvailabilityDiagnoser{byGroup: map[int64]service.ModelAvailabilityDiagnosis{
			sourceID: {HasAccountsInPool: true, HasModelSupport: false},
			targetID: {HasAccountsInPool: true, HasModelSupport: true},
		}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

		cls := classifyNoAccountErrorForGroupFromGin(
			c, diag, &targetID, "gpt-image-2", "gpt-image-2", service.PlatformOpenAI,
		)

		require.Equal(t, http.StatusServiceUnavailable, cls.Status)
		require.False(t, cls.ModelNotFound)
		require.Equal(t, []int64{targetID}, diag.calls)
	})
}
