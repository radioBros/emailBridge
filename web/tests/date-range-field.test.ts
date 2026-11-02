import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { computed, ref } from "vue";
import DateRangeField from "@/components/DateRangeField.vue";
import {
  formatRangeLabel,
  monthParts,
  shiftMonth,
  startOfMonth,
} from "@/components/dateRangeField";

const mdAndUp = ref(true);

vi.mock("vuetify", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vuetify")>();
  return {
    ...actual,
    useDisplay: () => ({
      mdAndUp: computed(() => mdAndUp.value),
      mobile: computed(() => !mdAndUp.value),
      name: computed(() => (mdAndUp.value ? "md" : "xs")),
      width: computed(() => (mdAndUp.value ? 1440 : 390)),
      height: computed(() => 900),
    }),
  };
});

const MenuStub = {
  name: "VMenu",
  props: { modelValue: Boolean },
  emits: ["update:modelValue"],
  template: `
    <div class="v-menu-stub">
      <slot name="activator" :props="{}" />
      <slot />
    </div>
  `,
};

const mountField = (props: { from?: string; to?: string; label?: string } = {}) =>
  mount(DateRangeField, {
    props: {
      from: props.from ?? "",
      to: props.to ?? "",
      label: props.label ?? "Date range",
    },
    global: {
      stubs: { VMenu: MenuStub },
    },
  });

describe("dateRangeField helpers", () => {
  it("formats full DD/MM/YYYY range labels", () => {
    expect(formatRangeLabel("2026-03-15T12:00:00.000Z", "2026-04-02T12:00:00.000Z")).toMatch(
      /^\d{2}\/\d{2}\/\d{4} – \d{2}\/\d{2}\/\d{4}$/,
    );
    const sameDay = new Date(2026, 2, 15, 12, 0, 0).toISOString();
    expect(formatRangeLabel(sameDay, sameDay)).toMatch(/^\d{2}\/\d{2}\/\d{4}$/);
    expect(formatRangeLabel("", "")).toBe("");
  });

  it("shifts months for dual-month navigation", () => {
    const anchor = startOfMonth(new Date(2026, 9, 15));
    expect(monthParts(anchor)).toEqual({ month: 9, year: 2026 });
    expect(monthParts(shiftMonth(anchor, 1))).toEqual({ month: 10, year: 2026 });
    expect(monthParts(shiftMonth(new Date(2026, 11, 1), 1))).toEqual({ month: 0, year: 2027 });
  });
});

describe("DateRangeField", () => {
  beforeEach(() => {
    mdAndUp.value = true;
  });

  it("shows full dates on the activator and fit-width class", async () => {
    const w = mountField({
      from: "2026-10-01T12:00:00.000Z",
      to: "2026-11-05T12:00:00.000Z",
    });
    await flushPromises();
    expect(w.classes()).toContain("date-range-field--fit");
    expect(w.find(".date-range-field__activator").classes()).not.toContain("flex-grow-1");
    expect(w.find(".date-range-field__activator").text()).toMatch(
      /\d{2}\/\d{2}\/\d{4} – \d{2}\/\d{2}\/\d{4}/,
    );
    expect(w.find(".date-range-field__clear").exists()).toBe(true);
    w.unmount();
  });

  it("puts the clear button inside the activator area and clears the range", async () => {
    const w = mountField({
      from: "2026-10-01T12:00:00.000Z",
      to: "2026-11-05T12:00:00.000Z",
      label: "Dates",
    });
    await flushPromises();
    expect(w.classes()).toContain("date-range-field--clearable");
    expect(w.classes()).toContain("position-relative");
    const clear = w.find('[data-testid="date-range-clear"]');
    expect(clear.attributes("aria-label")).toMatch(/clear/i);
    expect(clear.attributes("aria-label")).toContain("Dates");
    // Sibling of the activator, never a button nested in the activator button.
    expect(w.find(".date-range-field__activator [data-testid='date-range-clear']").exists()).toBe(
      false,
    );
    await clear.trigger("click");
    expect(w.emitted("update:from")?.at(-1)).toEqual([""]);
    expect(w.emitted("update:to")?.at(-1)).toEqual([""]);
    expect(w.emitted("change")).toHaveLength(1);
    w.unmount();

    const empty = mountField({ label: "Dates" });
    expect(empty.classes()).not.toContain("date-range-field--clearable");
    expect(empty.find('[data-testid="date-range-clear"]').exists()).toBe(false);
    empty.unmount();
  });

  it("keeps the label when empty", () => {
    const w = mountField({ label: "Pick dates" });
    expect(w.find(".date-range-field__activator").text()).toContain("Pick dates");
    w.unmount();
  });

  it("renders two adjacent months on desktop and navigates both", async () => {
    mdAndUp.value = true;
    const w = mountField();
    await flushPromises();
    expect(w.find('[data-testid="date-range-month-left"]').exists()).toBe(true);
    expect(w.find('[data-testid="date-range-month-right"]').exists()).toBe(true);

    const before = w.find(".date-range-field__controls-label").text();
    await w.find('[data-testid="next-month"]').trigger("click");
    await flushPromises();
    expect(w.find(".date-range-field__controls-label").text()).not.toBe(before);
    w.unmount();
  });

  it("shows a single month on phone", async () => {
    mdAndUp.value = false;
    const w = mountField();
    await flushPromises();
    expect(w.find('[data-testid="date-range-month-left"]').exists()).toBe(true);
    expect(w.find('[data-testid="date-range-month-right"]').exists()).toBe(false);
    w.unmount();
  });

  it("emits ISO bounds after a range across two months", async () => {
    mdAndUp.value = true;
    const w = mountField();
    await flushPromises();

    const dayButtons = (testid: string) =>
      w
        .find(`[data-testid="${testid}"]`)
        .findAll("button")
        .filter((b) => /^\d+$/.test(b.text().trim()));

    const leftDays = dayButtons("date-range-month-left");
    const rightDays = dayButtons("date-range-month-right");
    expect(leftDays.length).toBeGreaterThan(0);
    expect(rightDays.length).toBeGreaterThan(0);

    await leftDays[0]!.trigger("click");
    await flushPromises();
    // First click: from set, to cleared; change waits for the end date.
    const midTo = w.emitted("update:to");
    expect(String(midTo![midTo!.length - 1]![0])).toBe("");
    expect(w.emitted("change")).toBeUndefined();

    await w.setProps({
      from: String(w.emitted("update:from")!.at(-1)![0]),
      to: "",
    });
    await flushPromises();

    await rightDays[rightDays.length - 1]!.trigger("click");
    await flushPromises();

    const fromEv = w.emitted("update:from");
    const toEv = w.emitted("update:to");
    expect(fromEv?.length).toBeGreaterThan(0);
    expect(toEv?.length).toBeGreaterThan(0);

    const from = new Date(String(fromEv![fromEv!.length - 1]![0]));
    const toIso = String(toEv![toEv!.length - 1]![0]);
    expect(toIso).not.toBe("");
    const to = new Date(toIso);
    expect(from.getHours()).toBe(0);
    expect(from.getMinutes()).toBe(0);
    expect(to.getHours()).toBe(23);
    expect(to.getMinutes()).toBe(59);
    expect(from.getTime()).toBeLessThan(to.getTime());
    expect(w.emitted("change")?.length).toBe(1);
    w.unmount();
  });
});
