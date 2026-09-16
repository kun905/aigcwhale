import type { AdminGroup, GroupPlatform } from "@/types";

export interface ImageRoutingGroupOption {
  value: number | null;
  label: string;
  disabled?: boolean;
  [key: string]: unknown;
}

interface BuildImageRoutingGroupOptionsInput {
  groups: AdminGroup[];
  sourceGroupId?: number;
  selectedTargetId?: number | null;
  noneLabel: string;
  unavailableSuffix: string;
}

export function supportsImageGenerationRouting(
  platform: GroupPlatform | string,
): boolean {
  return platform === "openai";
}

export function normalizeImageGenerationGroupID(
  platform: GroupPlatform | string,
  value: number | null | undefined,
): number | null {
  if (!supportsImageGenerationRouting(platform)) return null;
  return typeof value === "number" && Number.isInteger(value) && value > 0
    ? value
    : null;
}

// UpdateGroup uses 0 as the explicit clear sentinel. JSON null means
// "field omitted / keep the current value" in the backend update contract.
export function serializeImageGenerationGroupIDForUpdate(
  platform: GroupPlatform | string,
  value: number | null | undefined,
): number {
  return normalizeImageGenerationGroupID(platform, value) ?? 0;
}

const isEligibleImageRoutingTarget = (
  group: AdminGroup,
  sourceGroupId?: number,
): boolean =>
  group.id !== sourceGroupId &&
  group.platform === "openai" &&
  group.status === "active" &&
  group.allow_image_generation === true &&
  group.image_generation_group_id == null;

export function buildImageRoutingGroupOptions({
  groups,
  sourceGroupId,
  selectedTargetId,
  noneLabel,
  unavailableSuffix,
}: BuildImageRoutingGroupOptionsInput): ImageRoutingGroupOption[] {
  const options: ImageRoutingGroupOption[] = [
    { value: null, label: noneLabel },
  ];

  const eligible = groups
    .filter((group) => isEligibleImageRoutingTarget(group, sourceGroupId))
    .sort((a, b) => a.name.localeCompare(b.name));

  for (const group of eligible) {
    options.push({ value: group.id, label: group.name });
  }

  const normalizedSelectedTargetId = normalizeImageGenerationGroupID(
    "openai",
    selectedTargetId,
  );
  if (
    normalizedSelectedTargetId !== null &&
    !options.some((option) => option.value === normalizedSelectedTargetId)
  ) {
    const staleTarget = groups.find(
      (group) => group.id === normalizedSelectedTargetId,
    );
    const label = staleTarget?.name ?? `#${normalizedSelectedTargetId}`;
    options.splice(1, 0, {
      value: normalizedSelectedTargetId,
      label: `${label} ${unavailableSuffix}`.trim(),
      disabled: true,
    });
  }

  return options;
}
