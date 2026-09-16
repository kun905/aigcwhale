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

// The routing target is deliberately a database-level self-reference.  This
// test verifies the safety property that deleting a target disables routing on
// source groups instead of leaving a dangling ID.
func TestGroupImageGenerationRouting_TargetDeleteClearsSource(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	source := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:     fmt.Sprintf("image-routing-source-%d", suffix),
		Platform: service.PlatformOpenAI,
	})
	target := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:     fmt.Sprintf("image-routing-target-%d", suffix),
		Platform: service.PlatformOpenAI,
	})

	cleanup := func() {
		// Hard-delete with a context that bypasses the Ent soft-delete hook so the
		// FK action is exercised.  The target is deleted first to test SET NULL.
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", source.ID, target.ID)
	}
	t.Cleanup(cleanup)

	_, err := integrationDB.ExecContext(ctx, `
		UPDATE groups
		SET allow_image_generation = TRUE,
			image_generation_group_id = $1
		WHERE id = $2`, target.ID, source.ID)
	require.NoError(t, err)

	var configured *int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT image_generation_group_id FROM groups WHERE id = $1", source.ID).Scan(&configured))
	require.NotNil(t, configured)
	require.Equal(t, target.ID, *configured)

	_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", target.ID)
	require.NoError(t, err)

	var cleared *int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT image_generation_group_id FROM groups WHERE id = $1", source.ID).Scan(&cleared))
	require.Nil(t, cleared, "deleting a routing target must disable the source route")
}

func TestGroupImageGenerationRouting_SoftDeletedTargetRemainsFailClosed(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	source := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:     fmt.Sprintf("image-routing-soft-source-%d", suffix),
		Platform: service.PlatformOpenAI,
	})
	target := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:     fmt.Sprintf("image-routing-soft-target-%d", suffix),
		Platform: service.PlatformOpenAI,
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id IN ($1, $2)", source.ID, target.ID)
	})

	_, err := integrationDB.ExecContext(ctx,
		"UPDATE groups SET image_generation_group_id = $1 WHERE id = $2", target.ID, source.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "UPDATE groups SET deleted_at = NOW() WHERE id = $1", target.ID)
	require.NoError(t, err)

	var retained *int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT image_generation_group_id FROM groups WHERE id = $1", source.ID).Scan(&retained))
	require.NotNil(t, retained, "soft deletion should retain the configured route for explicit fail-closed handling")
	require.Equal(t, target.ID, *retained)

	_, err = NewGroupRepository(integrationEntClient, integrationDB).GetByIDLite(ctx, target.ID)
	require.ErrorIs(t, err, service.ErrGroupNotFound, "normal repository lookup must hide the soft-deleted target")
}
