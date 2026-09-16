//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetByKeyForAuthCarriesImageGenerationGroupID(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	source := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("image-route-proj-source-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	target := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name: fmt.Sprintf("image-route-proj-target-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1,
	})
	_, err := integrationDB.ExecContext(ctx,
		"UPDATE groups SET allow_image_generation = TRUE, image_generation_group_id = $1 WHERE id = $2", target.ID, source.ID)
	require.NoError(t, err)

	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("image-route-proj-%d@example.com", suffix), Concurrency: 5,
	})
	sourceID := source.ID
	keyValue := fmt.Sprintf("sk-image-route-proj-%d", suffix)
	apiKeyRepo := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{UserID: user.ID, GroupID: &sourceID, Key: keyValue, Name: "image-route-proj", Status: service.StatusActive}
	require.NoError(t, apiKeyRepo.Create(ctx, key))
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", keyValue)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", source.ID, target.ID)
		require.NoError(t, cleanupErr)
	})

	got, err := apiKeyRepo.GetByKeyForAuth(ctx, keyValue)
	require.NoError(t, err)
	require.NotNil(t, got.Group, "authentication projection must carry the source group")
	require.Equal(t, &target.ID, got.Group.ImageGenerationGroupID,
		"missing this projected field would silently disable image routing after authentication")
}
