//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func (s *groupRepoStubForAdmin) HasImageGenerationRouteTo(_ context.Context, targetGroupID int64) (bool, error) {
	for _, candidate := range s.getByIDByID {
		if candidate != nil && candidate.ImageGenerationGroupID != nil && *candidate.ImageGenerationGroupID == targetGroupID {
			return true, nil
		}
	}
	return false, nil
}

func TestAdminService_CreateGroup_ImageGenerationTargetValidation(t *testing.T) {
	targetID := int64(20)
	chainedID := int64(30)
	cases := []struct {
		name           string
		sourcePlatform string
		target         *Group
		wantError      string
	}{
		{name: "valid", sourcePlatform: PlatformOpenAI, target: &Group{ID: targetID, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}},
		{name: "source is not openai", sourcePlatform: PlatformAnthropic, target: &Group{ID: targetID, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true}, wantError: "only supported for openai"},
		{name: "target is missing", sourcePlatform: PlatformOpenAI, wantError: "not found"},
		{name: "target is disabled", sourcePlatform: PlatformOpenAI, target: &Group{ID: targetID, Platform: PlatformOpenAI, Status: StatusDisabled, AllowImageGeneration: true}, wantError: "must be active"},
		{name: "target platform is wrong", sourcePlatform: PlatformOpenAI, target: &Group{ID: targetID, Platform: PlatformAnthropic, Status: StatusActive, AllowImageGeneration: true}, wantError: "must be openai"},
		{name: "target cannot generate images", sourcePlatform: PlatformOpenAI, target: &Group{ID: targetID, Platform: PlatformOpenAI, Status: StatusActive}, wantError: "must allow image generation"},
		{name: "target routes again", sourcePlatform: PlatformOpenAI, target: &Group{ID: targetID, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true, ImageGenerationGroupID: &chainedID}, wantError: "cannot route to another"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{}}
			if tc.target != nil {
				repo.getByIDByID[targetID] = tc.target
			}
			svc := &adminServiceImpl{groupRepo: repo}
			created, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name: "source", Platform: tc.sourcePlatform, RateMultiplier: 1, ImageGenerationGroupID: &targetID,
			})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.True(t, infraerrors.IsBadRequest(err), "configuration errors must map to HTTP 400")
				require.Equal(t, "INVALID_IMAGE_GENERATION_GROUP_ROUTE", infraerrors.Reason(err))
				require.Nil(t, created)
				require.Nil(t, repo.created)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, repo.created)
			require.Equal(t, &targetID, repo.created.ImageGenerationGroupID)
		})
	}
}

func TestAdminService_UpdateGroup_ImageGenerationTargetSetKeepAndClear(t *testing.T) {
	ctx := context.Background()
	sourceID, oldTargetID, newTargetID := int64(10), int64(20), int64(30)
	targets := map[int64]*Group{
		oldTargetID: {ID: oldTargetID, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
		newTargetID: {ID: newTargetID, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
	}
	for _, tc := range []struct {
		name       string
		input      UpdateGroupInput
		wantTarget *int64
		platform   string
	}{
		{name: "omitted target remains", input: UpdateGroupInput{}, wantTarget: &oldTargetID},
		{name: "replace target", input: UpdateGroupInput{ImageGenerationGroupID: &newTargetID}, wantTarget: &newTargetID},
		{name: "zero clears target", input: UpdateGroupInput{ImageGenerationGroupID: new(int64)}},
		{name: "platform change clears target", input: UpdateGroupInput{Platform: PlatformAnthropic}, platform: PlatformAnthropic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &Group{ID: sourceID, Name: "source", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1, ImageGenerationGroupID: &oldTargetID}
			repo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{sourceID: source, oldTargetID: targets[oldTargetID], newTargetID: targets[newTargetID]}}
			svc := &adminServiceImpl{groupRepo: repo}
			updated, err := svc.UpdateGroup(ctx, sourceID, &tc.input)
			require.NoError(t, err)
			require.Same(t, source, updated)
			require.Equal(t, tc.wantTarget, repo.updated.ImageGenerationGroupID)
			if tc.platform != "" {
				require.Equal(t, tc.platform, repo.updated.Platform)
			}
		})
	}
}

func TestAdminService_UpdateGroup_PreservesStaleTargetUntilExplicitlyCleared(t *testing.T) {
	sourceID, targetID := int64(10), int64(20)
	source := &Group{ID: sourceID, Name: "source", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1, ImageGenerationGroupID: &targetID}
	repo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
		sourceID: source,
		targetID: {ID: targetID, Platform: PlatformOpenAI, Status: StatusDisabled, AllowImageGeneration: true},
	}}
	svc := &adminServiceImpl{groupRepo: repo}

	// The admin form sends the current value back even when the operator edits
	// an unrelated field.  Keeping the same stale ID must therefore be allowed;
	// request-time routing will fail closed until the route is repaired.
	_, err := svc.UpdateGroup(context.Background(), sourceID, &UpdateGroupInput{ImageGenerationGroupID: &targetID})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	require.Equal(t, &targetID, repo.updated.ImageGenerationGroupID)

	zero := int64(0)
	_, err = svc.UpdateGroup(context.Background(), sourceID, &UpdateGroupInput{ImageGenerationGroupID: &zero})
	require.NoError(t, err)
	require.Nil(t, repo.updated.ImageGenerationGroupID)
}

func TestAdminService_UpdateGroup_ImageGenerationOneHopValidation(t *testing.T) {
	upstreamSourceID, currentID, newTargetID, terminalID := int64(10), int64(20), int64(30), int64(40)
	for _, tc := range []struct {
		name              string
		requestedTargetID int64
		upstreamRoutesTo  *int64
		targetRoutesTo    *int64
		wantError         string
	}{
		{
			name:              "existing target cannot become a source",
			requestedTargetID: newTargetID, upstreamRoutesTo: &currentID,
			wantError: "already used as an image generation target",
		},
		{
			name:              "reverse route is rejected",
			requestedTargetID: upstreamSourceID, upstreamRoutesTo: &currentID,
			wantError: "cannot route to another",
		},
		{
			name:              "self route is rejected",
			requestedTargetID: currentID,
			wantError:         "cannot set self",
		},
		{
			name:              "target with its own route is rejected",
			requestedTargetID: newTargetID, targetRoutesTo: &terminalID,
			wantError: "cannot route to another",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := &Group{
				ID: currentID, Name: "current", Platform: PlatformOpenAI,
				Status: StatusActive, RateMultiplier: 1, AllowImageGeneration: true,
			}
			repo := &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
				currentID: current,
				upstreamSourceID: {
					ID: upstreamSourceID, Platform: PlatformOpenAI, Status: StatusActive,
					AllowImageGeneration: true, ImageGenerationGroupID: tc.upstreamRoutesTo,
				},
				newTargetID: {
					ID: newTargetID, Platform: PlatformOpenAI, Status: StatusActive,
					AllowImageGeneration: true, ImageGenerationGroupID: tc.targetRoutesTo,
				},
			}}

			updated, err := (&adminServiceImpl{groupRepo: repo}).UpdateGroup(
				context.Background(), currentID, &UpdateGroupInput{ImageGenerationGroupID: &tc.requestedTargetID},
			)

			require.ErrorContains(t, err, tc.wantError)
			require.True(t, infraerrors.IsBadRequest(err), "configuration errors must map to HTTP 400")
			require.Equal(t, "INVALID_IMAGE_GENERATION_GROUP_ROUTE", infraerrors.Reason(err))
			require.Nil(t, updated)
			require.Nil(t, repo.updated, "invalid one-hop route must not be persisted")
			require.Nil(t, current.ImageGenerationGroupID, "failed validation must leave the current route unchanged")
		})
	}
}

type imageRoutingRepoErrorStub struct {
	*groupRepoStubForAdmin
	getByIDLiteErr error
	hasRouteErr    error
}

func (s *imageRoutingRepoErrorStub) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	if s.getByIDLiteErr != nil {
		return nil, s.getByIDLiteErr
	}
	return s.groupRepoStubForAdmin.GetByIDLite(ctx, id)
}

func (s *imageRoutingRepoErrorStub) HasImageGenerationRouteTo(_ context.Context, _ int64) (bool, error) {
	if s.hasRouteErr != nil {
		return false, s.hasRouteErr
	}
	return false, nil
}

func TestAdminService_ImageGenerationTargetRepositoryErrorsRemainInternal(t *testing.T) {
	t.Run("target lookup error", func(t *testing.T) {
		targetID := int64(20)
		repo := &imageRoutingRepoErrorStub{
			groupRepoStubForAdmin: &groupRepoStubForAdmin{},
			getByIDLiteErr:        errors.New("database unavailable"),
		}

		_, err := (&adminServiceImpl{groupRepo: repo}).CreateGroup(context.Background(), &CreateGroupInput{
			Name: "source", Platform: PlatformOpenAI, RateMultiplier: 1, ImageGenerationGroupID: &targetID,
		})

		require.ErrorContains(t, err, "get image generation target group")
		require.Equal(t, 500, infraerrors.Code(err))
	})

	t.Run("reverse route lookup error", func(t *testing.T) {
		currentID, targetID := int64(10), int64(20)
		repo := &imageRoutingRepoErrorStub{
			groupRepoStubForAdmin: &groupRepoStubForAdmin{getByIDByID: map[int64]*Group{
				currentID: {ID: currentID, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1},
				targetID:  {ID: targetID, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
			}},
			hasRouteErr: errors.New("database unavailable"),
		}

		_, err := (&adminServiceImpl{groupRepo: repo}).UpdateGroup(
			context.Background(), currentID, &UpdateGroupInput{ImageGenerationGroupID: &targetID},
		)

		require.ErrorContains(t, err, "check image generation route references")
		require.Equal(t, 500, infraerrors.Code(err))
	})
}
