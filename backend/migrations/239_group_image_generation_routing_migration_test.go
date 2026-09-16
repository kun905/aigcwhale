package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration239AddsImageGenerationGroupRouting(t *testing.T) {
	sqlText, err := FS.ReadFile("239_group_image_generation_routing.sql")
	require.NoError(t, err)
	migration := strings.ToLower(string(sqlText))

	require.Contains(t, migration, "add column if not exists image_generation_group_id bigint")
	require.Contains(t, migration, "references groups(id) on delete set null")
	require.Contains(t, migration, "create index if not exists idx_groups_image_generation_group_id")
	require.Contains(t, migration, "comment on column groups.image_generation_group_id")
	require.Contains(t, migration, "old.image_generation_group_id is not distinct from new.image_generation_group_id")
}

func TestMigration239PreservesMigration193GroupTriggerWatchList(t *testing.T) {
	previousSQL, err := FS.ReadFile("193_group_profit_control_auth_cache_invalidation.sql")
	require.NoError(t, err)
	currentSQL, err := FS.ReadFile("239_group_image_generation_routing.sql")
	require.NoError(t, err)

	previous := migrationGroupTriggerWatchFields(t, string(previousSQL))
	current := migrationGroupTriggerWatchFields(t, string(currentSQL))
	require.NotEmpty(t, previous, "migration 193 must define a non-empty group trigger watch list")
	require.NotEmpty(t, current, "migration 239 must define a non-empty group trigger watch list")
	for field := range previous {
		require.Contains(t, current, field, "migration 239 must preserve migration 193 trigger field %q", field)
	}
	require.Contains(t, current, "image_generation_group_id", "migration 239 must add the new routing field to the trigger watch list")
}

func migrationGroupTriggerWatchFields(t *testing.T, sqlText string) map[string]struct{} {
	t.Helper()
	pairs := regexp.MustCompile(`(?i)OLD\.([a-z0-9_]+)\s+IS\s+NOT\s+DISTINCT\s+FROM\s+NEW\.([a-z0-9_]+)`).FindAllStringSubmatch(sqlText, -1)
	fields := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		require.Len(t, pair, 3)
		require.Equal(t, strings.ToLower(pair[1]), strings.ToLower(pair[2]), "trigger comparison must compare the same OLD/NEW field")
		fields[strings.ToLower(pair[1])] = struct{}{}
	}
	return fields
}
