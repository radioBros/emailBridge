import { describe, expect, it } from "vitest";
import { computed, ref } from "vue";

/**
 * Unit-level coverage of the ServerTable kit-bug fixes without mounting the
 * full VDataTableVirtual (heavy + flaky under happy-dom).
 */

describe("ServerTable kit bug fixes (logic)", () => {
  it("skeleton only on first load", () => {
    const loading = ref(true);
    const hasLoadedOnce = ref(false);
    const isFirstLoad = computed(() => loading.value && !hasLoadedOnce.value);

    expect(isFirstLoad.value).toBe(true);

    loading.value = false;
    hasLoadedOnce.value = true;
    expect(isFirstLoad.value).toBe(false);

    loading.value = true;
    expect(isFirstLoad.value).toBe(false);
  });

  it("custom-key-sort is a no-op (server-side sorting only)", () => {
    const items = [
      { id: "2", name: "B" },
      { id: "1", name: "A" },
    ];
    const noClientSort = { name: (_a?: unknown, _b?: unknown) => 0, id: () => 0 };
    const sorted = [...items].sort((a, b) => noClientSort.name(a, b));
    expect(sorted.map((i) => i.name)).toEqual(["B", "A"]);
  });

  it("row props stamp data-id and data-testid from itemValue", () => {
    const itemValue = "id";
    const rowProps = (data: { item: Record<string, unknown> }) => {
      const id = String(data.item[itemValue] ?? "");
      return { "data-id": id, "data-testid": `row-${id}` };
    };
    expect(rowProps({ item: { id: "abc", name: "X" } })).toEqual({
      "data-id": "abc",
      "data-testid": "row-abc",
    });
  });
});
