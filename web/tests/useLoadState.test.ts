import { describe, expect, it } from "vitest";
import { useLoadState } from "@/composables/useLoadState";

describe("useLoadState", () => {
  it("first load vs refresh are mutually exclusive", () => {
    const state = useLoadState();
    expect(state.isFirstLoad.value).toBe(false);
    expect(state.isRefreshing.value).toBe(false);

    state.start();
    expect(state.isFirstLoad.value).toBe(true);
    expect(state.isRefreshing.value).toBe(false);

    state.finish(true);
    state.start();
    expect(state.isFirstLoad.value).toBe(false);
    expect(state.isRefreshing.value).toBe(true);
  });

  it("failed first load stays a first load", () => {
    const state = useLoadState();
    state.start();
    state.finish(false);
    state.start();
    expect(state.isFirstLoad.value).toBe(true);
  });
});
