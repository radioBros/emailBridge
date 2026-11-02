import { describe, expect, it } from "vitest";
import { sortAndPage } from "@/features/cluster/helpers/sortPage";

describe("sortAndPage", () => {
  const rows = [
    { id: "c", accounts: 2, seenAt: "2026-10-26T10:00:00Z", version: "0.2.0" },
    { id: "a", accounts: 10, seenAt: "2026-10-26T12:00:00Z", version: "0.1.0" },
    { id: "b", accounts: 5, seenAt: "2026-10-26T11:00:00Z", version: "0.3.0" },
  ];

  it("sorts strings, numbers and dates", () => {
    const byId = sortAndPage(rows, [{ key: "id", order: "asc" }], 1, 25);
    expect(byId.rows.map((r) => r.id)).toEqual(["a", "b", "c"]);

    const byAccounts = sortAndPage(
      rows,
      [{ key: "accounts", order: "desc" }],
      1,
      25,
      { numberKeys: ["accounts"] },
    );
    expect(byAccounts.rows.map((r) => r.accounts)).toEqual([10, 5, 2]);

    const bySeen = sortAndPage(
      rows,
      [{ key: "seenAt", order: "asc" }],
      1,
      25,
      { dateKeys: ["seenAt"] },
    );
    expect(bySeen.rows.map((r) => r.id)).toEqual(["c", "b", "a"]);
  });

  it("pages after sort", () => {
    const page1 = sortAndPage(
      rows,
      [{ key: "id", order: "asc" }],
      1,
      2,
    );
    expect(page1.total).toBe(3);
    expect(page1.rows.map((r) => r.id)).toEqual(["a", "b"]);

    const page2 = sortAndPage(
      rows,
      [{ key: "id", order: "asc" }],
      2,
      2,
    );
    expect(page2.rows.map((r) => r.id)).toEqual(["c"]);
  });

  it("returns unsorted snapshot when no sort", () => {
    const out = sortAndPage(rows, [], 1, 10);
    expect(out.rows.map((r) => r.id)).toEqual(["c", "a", "b"]);
  });
});
