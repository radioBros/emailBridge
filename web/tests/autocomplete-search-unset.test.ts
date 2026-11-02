import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import { VSelect } from "vuetify/components";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";

// W67: filter refs often start as "" — Vuetify counts that as dirty and showed the clear (x) on an
// empty field. AutocompleteSearch treats "" as unset unless an item really carries "" ("All").

const mountSearch = (modelValue: string | null, items: Array<{ title: string; value: string }>) =>
  mount(AutocompleteSearch, {
    props: { modelValue, items, clearable: true, label: "Account" },
  });

const accounts = [
  { title: "Support", value: "acc_1" },
  { title: "Billing", value: "acc_2" },
];

describe("AutocompleteSearch empty model", () => {
  it('passes null to the select for "" when no item has an empty value', () => {
    const wrapper = mountSearch("", accounts);
    expect(wrapper.findComponent(VSelect).props("modelValue")).toBeNull();
    // Vuetify shows the clear icon only on a dirty field.
    expect(wrapper.find(".v-field").classes()).not.toContain("v-field--dirty");
    expect(wrapper.find(".select-search").classes()).not.toContain("has-value");
  });

  it('keeps "" when an "All" item carries the empty value', () => {
    const wrapper = mountSearch("", [{ title: "All", value: "" }, ...accounts]);
    expect(wrapper.findComponent(VSelect).props("modelValue")).toBe("");
    expect(wrapper.find(".select-search").classes()).toContain("has-value");
  });

  it("keeps a real value (dirty field, clear icon shown)", () => {
    const wrapper = mountSearch("acc_1", accounts);
    expect(wrapper.findComponent(VSelect).props("modelValue")).toBe("acc_1");
    expect(wrapper.find(".v-field").classes()).toContain("v-field--dirty");
  });

  it("treats null as unset", () => {
    const wrapper = mountSearch(null, accounts);
    expect(wrapper.findComponent(VSelect).props("modelValue")).toBeNull();
    // Vuetify shows the clear icon only on a dirty field.
    expect(wrapper.find(".v-field").classes()).not.toContain("v-field--dirty");
  });
});
