//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageRoutingHTTPGroupRepo struct {
	service.AdminGroupRepository
	groups          map[int64]*service.Group
	targetLookupErr error
	hasRouteTo      bool
	hasRouteErr     error
}

func (r *imageRoutingHTTPGroupRepo) GetByID(_ context.Context, id int64) (*service.Group, error) {
	group, ok := r.groups[id]
	if !ok {
		return nil, service.ErrGroupNotFound
	}
	return group, nil
}

func (r *imageRoutingHTTPGroupRepo) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if r.targetLookupErr != nil {
		return nil, r.targetLookupErr
	}
	group, ok := r.groups[id]
	if !ok {
		return nil, service.ErrGroupNotFound
	}
	return group, nil
}

func (r *imageRoutingHTTPGroupRepo) Create(_ context.Context, _ *service.Group) error {
	return nil
}

func (r *imageRoutingHTTPGroupRepo) Update(_ context.Context, _ *service.Group) error {
	return nil
}

func (r *imageRoutingHTTPGroupRepo) HasImageGenerationRouteTo(_ context.Context, _ int64) (bool, error) {
	return r.hasRouteTo, r.hasRouteErr
}

func newImageRoutingHTTPRouter(repo service.AdminGroupRepository) *gin.Engine {
	adminService := service.NewAdminService(
		nil,
		nil,
		repo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	handler := NewGroupHandler(adminService, nil, nil)
	router := gin.New()
	router.POST("/groups", handler.Create)
	router.PUT("/groups/:id", handler.Update)
	return router
}

func TestGroupHandlerImageRoutingValidationHTTPStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const (
		currentID  = int64(10)
		targetID   = int64(20)
		terminalID = int64(30)
	)

	activeCurrent := func() *service.Group {
		return &service.Group{ID: currentID, Name: "source", Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1}
	}
	activeTarget := func() *service.Group {
		return &service.Group{ID: targetID, Name: "images", Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true}
	}
	createBody := func(platform string) string {
		return `{"name":"source","platform":"` + platform + `","rate_multiplier":1,"subscription_type":"standard","image_generation_group_id":20}`
	}
	updateBody := `{"image_generation_group_id":20}`

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		repo        *imageRoutingHTTPGroupRepo
		wantStatus  int
		wantMessage string
	}{
		{
			name: "source platform is invalid", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformAnthropic),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{targetID: activeTarget()}},
			wantStatus: http.StatusBadRequest, wantMessage: "image generation routing only supported for openai groups",
		},
		{
			name: "target is missing", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformOpenAI),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{}},
			wantStatus: http.StatusBadRequest, wantMessage: "image generation target group not found",
		},
		{
			name: "target is disabled", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformOpenAI),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{targetID: {ID: targetID, Platform: service.PlatformOpenAI, Status: service.StatusDisabled, AllowImageGeneration: true}}},
			wantStatus: http.StatusBadRequest, wantMessage: "image generation target group must be active",
		},
		{
			name: "target platform is invalid", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformOpenAI),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{targetID: {ID: targetID, Platform: service.PlatformAnthropic, Status: service.StatusActive, AllowImageGeneration: true}}},
			wantStatus: http.StatusBadRequest, wantMessage: "image generation target group must be openai platform",
		},
		{
			name: "target cannot generate images", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformOpenAI),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{targetID: {ID: targetID, Platform: service.PlatformOpenAI, Status: service.StatusActive}}},
			wantStatus: http.StatusBadRequest, wantMessage: "image generation target group must allow image generation",
		},
		{
			name: "target has another hop", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformOpenAI),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{targetID: {ID: targetID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowImageGeneration: true, ImageGenerationGroupID: pointerToInt64(terminalID)}}},
			wantStatus: http.StatusBadRequest, wantMessage: "image generation target group cannot route to another image generation group",
		},
		{
			name: "self route", method: http.MethodPut, path: "/groups/10", body: `{"image_generation_group_id":10}`,
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{currentID: activeCurrent()}},
			wantStatus: http.StatusBadRequest, wantMessage: "cannot set self as image generation target group",
		},
		{
			name: "reverse route", method: http.MethodPut, path: "/groups/10", body: updateBody,
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{currentID: activeCurrent(), targetID: activeTarget()}, hasRouteTo: true},
			wantStatus: http.StatusBadRequest, wantMessage: "group already used as an image generation target cannot route to another image generation group",
		},
		{
			name: "target repository failure", method: http.MethodPost, path: "/groups", body: createBody(service.PlatformOpenAI),
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{}, targetLookupErr: errors.New("database unavailable")},
			wantStatus: http.StatusInternalServerError, wantMessage: "internal error",
		},
		{
			name: "reverse route repository failure", method: http.MethodPut, path: "/groups/10", body: updateBody,
			repo:       &imageRoutingHTTPGroupRepo{groups: map[int64]*service.Group{currentID: activeCurrent(), targetID: activeTarget()}, hasRouteErr: errors.New("database unavailable")},
			wantStatus: http.StatusInternalServerError, wantMessage: "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			newImageRoutingHTTPRouter(tc.repo).ServeHTTP(recorder, req)

			require.Equal(t, tc.wantStatus, recorder.Code)
			var envelope response.Response
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
			require.Equal(t, tc.wantStatus, envelope.Code)
			require.Equal(t, tc.wantMessage, envelope.Message)
			if tc.wantStatus == http.StatusBadRequest {
				require.Equal(t, "INVALID_IMAGE_GENERATION_GROUP_ROUTE", envelope.Reason)
			} else {
				require.Empty(t, envelope.Reason)
			}
		})
	}
}

func pointerToInt64(value int64) *int64 {
	return &value
}
