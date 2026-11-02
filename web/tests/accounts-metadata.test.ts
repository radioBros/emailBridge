import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import { nextTick } from "vue";
import AccountMetadataEditor from "@/features/accounts/components/AccountMetadataEditor.vue";
import {
  METADATA_LIMITS,
  emptyMetadataRow,
  metadataFieldErrors,
  metadataFromRows,
  metadataMergePatch,
  rowsFromMetadata,
  validateMetadataRows,
  type MetadataRow,
} from "@/features/accounts/helpers/accountMetadata";
import { emptyForm, formFromAccount, toCreateInput, toPatchBody } from "@/features/accounts/helpers/accountForm";
import type { AccountView } from "@/api/accounts";

const row = (key: string, type: MetadataRow["type"], value: string): MetadataRow => ({
  ...emptyMetadataRow(),
  key,
  type,
  value,
});

describe("accountMetadata helpers", () => {
  it("round-trips metadata through rows with types", () => {
    const meta = { "crm.id": "48213", tier: 2, vip: true, legacy: null };
    const rows = rowsFromMetadata(meta);
    expect(rows.map((r) => [r.key, r.type, r.value])).toEqual([
      ["crm.id", "string", "48213"],
      ["legacy", "null", ""],
      ["tier", "number", "2"],
      ["vip", "boolean", "true"],
    ]);
    expect(metadataFromRows(rows)).toEqual(meta);
  });

  it("skips blank rows and validates keys, duplicates and values", () => {
    const rows = [
      row("", "string", ""),
      row("bad key", "string", "x"),
      row("dup", "string", "a"),
      row("dup", "string", "b"),
      row("n", "number", "abc"),
      row("long", "string", "é".repeat(513)),
      row("", "string", "orphan value"),
    ];
    const v = validateMetadataRows(rows);
    expect(v.valid).toBe(false);
    expect(v.rows[rows[0]!.id]).toBeUndefined();
    expect(v.rows[rows[1]!.id]?.key).toBe("key_invalid");
    expect(v.rows[rows[2]!.id]).toBeUndefined();
    expect(v.rows[rows[3]!.id]?.key).toBe("key_duplicate");
    expect(v.rows[rows[4]!.id]?.value).toBe("value_number");
    expect(v.rows[rows[5]!.id]?.value).toBe("value_too_long");
    expect(v.rows[rows[6]!.id]?.key).toBe("key_required");
    expect(validateMetadataRows([row("ok", "number", "1.5")]).valid).toBe(true);
  });

  it("enforces the key count and the serialised size", () => {
    const many = Array.from({ length: METADATA_LIMITS.maxKeys + 1 }, (_, i) => row(`k${i}`, "string", "v"));
    expect(validateMetadataRows(many).total).toBe("too_many");
    const big = Array.from({ length: 10 }, (_, i) => row(`k${i}`, "string", "v".repeat(1000)));
    expect(validateMetadataRows(big).total).toBe("too_large");
  });

  it("builds a JSON Merge Patch with removals as null and nothing for unchanged keys", () => {
    const before = { crm: "a", tier: 1, old: "x", gone: null };
    expect(metadataMergePatch(before, { crm: "a", tier: 2, fresh: true, gone: null })).toEqual({
      tier: 2,
      fresh: true,
      old: null,
    });
    expect(metadataMergePatch(before, { ...before })).toBeUndefined();
    expect(metadataMergePatch(undefined, {})).toBeUndefined();
    // A renamed key is a removal plus an addition.
    expect(metadataMergePatch({ a: "1" }, { b: "1" })).toEqual({ a: null, b: "1" });
  });

  it("maps problem field paths to metadata keys", () => {
    expect(
      metadataFieldErrors({ "body.metadata.crm.id": "bad", "body.metadata": "too big", "body.name": "x" }),
    ).toEqual({ "crm.id": "bad", "": "too big" });
  });

  it("sends metadata on create and as a merge patch on edit", () => {
    const f = emptyForm();
    expect(toCreateInput(f)).not.toHaveProperty("metadata");
    f.metadata = [row("crm", "string", "x"), row("tier", "number", "3")];
    expect((toCreateInput(f) as { metadata?: unknown }).metadata).toEqual({ crm: "x", tier: 3 });

    const account = {
      id: "acc_1",
      name: "A",
      email: "a@example.test",
      state: "connected",
      imap: { host: "i", port: 993, tls: "implicit", auth: { user: "u", hasPassword: true } },
      smtp: { host: "s", port: 465, tls: "implicit", auth: { user: "u", hasPassword: true } },
      sync: {},
      version: 2,
      metadata: { crm: "x", old: "y" },
    } as unknown as AccountView;
    const edit = formFromAccount(account);
    expect(toPatchBody(edit, { crm: "x", old: "y" })).not.toHaveProperty("metadata");
    edit.metadata = edit.metadata.filter((r) => r.key !== "old");
    expect((toPatchBody(edit, { crm: "x", old: "y" }) as { metadata?: unknown }).metadata).toEqual({ old: null });
  });
});

describe("AccountMetadataEditor", () => {
  const mountEditor = (rows: MetadataRow[], serverErrors: Record<string, string> = {}) =>
    mount(AccountMetadataEditor, { props: { modelValue: rows, serverErrors } });

  it("shows an empty state and adds a row", async () => {
    const w = mountEditor([]);
    expect(w.findAll('[data-testid="metadata-row"]')).toHaveLength(0);
    await w.find('[data-testid="metadata-add"]').trigger("click");
    const emitted = w.emitted("update:modelValue")?.[0]?.[0] as MetadataRow[];
    expect(emitted).toHaveLength(1);
    expect(emitted[0]).toMatchObject({ key: "", type: "string", value: "" });
  });

  it("renders rows, edits a key and removes a row", async () => {
    const rows = rowsFromMetadata({ crm: "x", tier: 2 });
    const w = mountEditor(rows);
    expect(w.findAll('[data-testid="metadata-row"]')).toHaveLength(2);
    const keyInput = w.find('[data-testid="metadata-row"] input');
    await keyInput.setValue("crm2");
    const edited = w.emitted("update:modelValue")?.at(-1)?.[0] as MetadataRow[];
    expect(edited[0]?.key).toBe("crm2");
    expect(edited[1]).toEqual(rows[1]);

    await w.findAll('[data-testid="metadata-remove"]')[1]!.trigger("click");
    const removed = w.emitted("update:modelValue")?.at(-1)?.[0] as MetadataRow[];
    expect(removed.map((r) => r.key)).toEqual(["crm"]);
  });

  it("shows validation messages and server errors", async () => {
    const rows = [row("bad key", "string", "x"), row("tier", "number", "abc"), row("crm.id", "string", "1")];
    const w = mountEditor(rows, { "crm.id": "Server says no", "": "Too big on the server" });
    await nextTick();
    const text = w.text();
    expect(text).toContain("accounts.metadata_err_key_invalid");
    expect(text).toContain("accounts.metadata_err_value_number");
    expect(text).toContain("Server says no");
    expect(w.find('[data-testid="metadata-total-error"]').text()).toContain("Too big on the server");
  });

  it("disables add at the key limit", () => {
    const rows = Array.from({ length: METADATA_LIMITS.maxKeys }, (_, i) => row(`k${i}`, "string", "v"));
    const w = mountEditor(rows);
    expect(w.find('[data-testid="metadata-add"]').attributes("disabled")).toBeDefined();
  });
});

describe("AccountMetadataEditor null type", () => {
  const typeValues = (w: ReturnType<typeof mount>, index: number): string[] => {
    const select = w.findAllComponents({ name: "VSelect" })[index]!;
    return (select.props("items") as { value: string }[]).map((i) => i.value);
  };

  it("offers null only on create or for a row that is already null", () => {
    const rows = [row("a", "string", "x"), row("b", "null", "")];
    const edit = mount(AccountMetadataEditor, { props: { modelValue: rows } });
    expect(typeValues(edit, 0)).not.toContain("null");
    expect(typeValues(edit, 1)).toContain("null");
    const create = mount(AccountMetadataEditor, { props: { modelValue: rows, allowNull: true } });
    expect(typeValues(create, 0)).toContain("null");
  });
});
