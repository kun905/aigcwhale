package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupMapperExposesImageGenerationTargetOnlyToAdmins(t *testing.T) {
	targetID := int64(42)
	group := &service.Group{
		ID:                     7,
		Name:                   "gpt-with-image-route",
		Platform:               service.PlatformOpenAI,
		Status:                 service.StatusActive,
		ImageGenerationGroupID: &targetID,
	}

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "image_generation_group_id")

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"image_generation_group_id":42`)
}
