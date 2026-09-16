import { describe, expect, it } from "vitest";

import type { AdminGroup } from "@/types";
import {
  buildImageRoutingGroupOptions,
  normalizeImageGenerationGroupID,
  serializeImageGenerationGroupIDForUpdate,
} from "../groupsImageRouting";

const group = (overrides: Partial<AdminGroup>): AdminGroup =>
  ({
    id: 1,
    name: "Image Pool",
    platform: "openai",
    status: "active",
    allow_image_generation: true,
    image_generation_group_id: null,
    ...overrides,
  }) as AdminGroup;

describe("groups image-generation routing", () => {
  it("keeps a selected target when creating an OpenAI group", () => {
    expect(normalizeImageGenerationGroupID("openai", 27)).toBe(27);
    expect(normalizeImageGenerationGroupID("openai", null)).toBeNull();
  });

  it("serializes edit clearing as the backend's explicit zero sentinel", () => {
    expect(serializeImageGenerationGroupIDForUpdate("openai", null)).toBe(0);
    expect(serializeImageGenerationGroupIDForUpdate("openai", 27)).toBe(27);
  });

  it("clears a stale selection when the create platform switches away from OpenAI", () => {
    expect(normalizeImageGenerationGroupID("anthropic", 27)).toBeNull();
    expect(normalizeImageGenerationGroupID("composite", 27)).toBeNull();
  });

  it("offers only active one-hop OpenAI image groups and excludes the source group", () => {
    const options = buildImageRoutingGroupOptions({
      groups: [
        group({ id: 1, name: "Zulu" }),
        group({ id: 2, name: "Alpha" }),
        group({ id: 3, name: "Inactive", status: "inactive" }),
        group({ id: 4, name: "No Images", allow_image_generation: false }),
        group({ id: 5, name: "Anthropic", platform: "anthropic" }),
        group({ id: 6, name: "Chained", image_generation_group_id: 99 }),
      ],
      sourceGroupId: 1,
      noneLabel: "None",
      unavailableSuffix: "(unavailable)",
    });

    expect(options).toEqual([
      { value: null, label: "None" },
      { value: 2, label: "Alpha" },
    ]);
  });

  it("keeps an invalid historical edit target visible and disabled so it can be cleared", () => {
    const options = buildImageRoutingGroupOptions({
      groups: [
        group({ id: 2, name: "Healthy" }),
        group({ id: 9, name: "Old Pool", status: "inactive" }),
      ],
      sourceGroupId: 1,
      selectedTargetId: 9,
      noneLabel: "None",
      unavailableSuffix: "(unavailable)",
    });

    expect(options).toEqual([
      { value: null, label: "None" },
      {
        value: 9,
        label: "Old Pool (unavailable)",
        disabled: true,
      },
      { value: 2, label: "Healthy" },
    ]);
  });

  it("shows a defensive id label when a historical target is missing", () => {
    expect(
      buildImageRoutingGroupOptions({
        groups: [],
        selectedTargetId: 404,
        noneLabel: "None",
        unavailableSuffix: "(unavailable)",
      }),
    ).toEqual([
      { value: null, label: "None" },
      { value: 404, label: "#404 (unavailable)", disabled: true },
    ]);
  });
});
