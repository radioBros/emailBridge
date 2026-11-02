import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import {
  ACCOUNT_FIELDS,
  EVENT_TYPES,
  childFields,
  dataFieldsFor,
  eventTypeKey,
} from "@/features/rules/catalog/catalog";
import {
  VIRTUAL_INBOX,
  accountIdsIn,
  defOf,
  depthOf,
  eventTypesIn,
  folderLeafFor,
  folderViewOf,
  isComplete,
  isGroup,
  isPayloadPath,
  keyOf,
  newLeaf,
  normalizeForCompile,
  valueForOp,
} from "@/features/rules/model/builder";
import {
  classifyLeaf,
  duplicateKeys,
  freshKey,
  isVisual,
  nodeOfKind,
  parseMap,
  secretsIn,
  startingMap,
  toCel,
  type MapNode,
} from "@/features/rules/model/mapTree";
import { applyTemplate, completeCel } from "@/features/rules/model/completions";
import {
  SNIPPETS,
  formatCel,
  offsetOf,
  payloadNeeds,
  positionInMessage,
  splitTopLevel,
} from "@/features/rules/model/code";
import { DEFAULT_SECRET_NAME, PRESETS, presetById, keyedInboundMap } from "@/features/rules/model/presets";

const LEGACY_APP_MAP = `{
  "key": env.webhookKey,
  "body": {
    "id": data.messageId,
    "date": event.time,
    "account": account.externalId != "" ? account.externalId : account.id,
    "event": "messageNew",
    "headers": data.headers,
    "emailid": data.messageIdHeader,
    "subject": data.subject
  }
}`;

describe("rules catalogue", () => {
  it("is generated from api/events and up to date", () => {
    expect(() => execFileSync("node", ["scripts/gen-rule-catalog.mjs", "--check"], { stdio: "pipe" })).not.toThrow();
  });

  it("lists real event types without synthetic ones", () => {
    expect(EVENT_TYPES).toContain("message.received");
    expect(EVENT_TYPES).toContain("pec.receipt");
    expect(EVENT_TYPES).toContain("message.bounced");
    expect(EVENT_TYPES).not.toContain("endpoint.test");
    expect(eventTypeKey("message.delivery_attempt_failed")).toBe("rules.event.message_delivery_attempt_failed");
  });

  it("knows payload fields with types and descriptions", () => {
    const fields = dataFieldsFor(["message.received"]);
    const subject = fields.find((f) => f.path === "data.subject");
    expect(subject?.type).toBe("string");
    expect(fields.find((f) => f.path === "data.size")?.type).toBe("number");
    expect(fields.find((f) => f.path === "data.labels")?.items).toBe("string");
    expect(fields.find((f) => f.path === "data.messageIdHeader")?.description).toMatch(/Message-ID/);
    expect(childFields("data.mailbox", ["message.received"]).map((f) => f.path)).toEqual([
      "data.mailbox.mailboxId",
      "data.mailbox.path",
      "data.mailbox.specialUse",
    ]);
    expect(childFields("account", []).length).toBe(ACCOUNT_FIELDS.length);
    // Union of the outbound types when several are chosen.
    const out = dataFieldsFor(["message.sent", "message.failed"]).map((f) => f.path);
    expect(out).toContain("data.smtp.response");
    expect(out).toContain("data.error.kind");
  });
});

describe("builder model", () => {
  it("creates leaves with sensible values per field", () => {
    expect(newLeaf("type")).toEqual({ field: "type", op: "in", value: ["message.received"] });
    expect(newLeaf("isInbox")).toEqual({ field: "isInbox", op: "eq", value: true });
    expect(newLeaf("subject")).toEqual({ field: "subject", op: "eq", value: "" });
    const hour = newLeaf("hour");
    expect(hour.name).toBeTruthy();
    expect(newLeaf("businessHours").value).toMatchObject({ window: "09:00-18:00", days: "Mon-Fri" });
    expect(newLeaf("data.size", ["message.received"])).toEqual({ field: "data.size", op: "eq", value: 0 });
    expect(newLeaf("data.bounce", ["message.received"]).op).toBe("exists");
  });

  it("picks ops by schema type", () => {
    expect(defOf("data.labels", ["message.received"]).ops).toContain("includes");
    expect(defOf("data.seenBefore", ["message.received"]).value).toBe("bool");
    expect(defOf("data.error.stage", ["message.failed"]).enumValues).toContain("rcptTo");
    expect(defOf("data.unknown").value).toBe("text");
    expect(isPayloadPath("data.mailbox.path")).toBe(true);
    expect(isPayloadPath("subject")).toBe(false);
    expect(isPayloadPath("account.id")).toBe(false); // a fixed field
    expect(isPayloadPath("data.x) || (true")).toBe(false);
  });

  it("converts values when the op changes", () => {
    expect(valueForOp({ field: "subject", op: "eq", value: "a" }, "in")).toEqual(["a"]);
    expect(valueForOp({ field: "subject", op: "in", value: ["a", "b"] }, "eq")).toBe("a");
    expect(valueForOp({ field: "subject", op: "eq", value: "a" }, "exists")).toBeUndefined();
    expect(valueForOp({ field: "subject", op: "eq", value: "" }, "in")).toEqual([]);
  });

  it("normalises the tree for the compile API", () => {
    const tree = {
      match: "all" as const,
      conditions: [
        { field: "type", op: "in", value: ["message.received"] },
        { field: "subject", op: "contains", value: "" },
        { match: "any" as const, conditions: [] },
        {
          match: "not" as const,
          conditions: [{ field: "header", name: "", op: "exists" }, { field: "isAutoReply", op: "eq", value: true }],
        },
      ],
    };
    expect(normalizeForCompile(tree)).toEqual({
      match: "all",
      conditions: [
        { field: "type", op: "eq", value: "message.received" },
        { match: "not", conditions: [{ field: "isAutoReply", op: "eq", value: true }] },
      ],
    });
    expect(tree.conditions[0]).toEqual({ field: "type", op: "in", value: ["message.received"] });
    expect(isComplete({ field: "inList", op: "eq", name: "vip" })).toBe(true);
    expect(isComplete({ field: "inList", op: "eq" })).toBe(false);
    expect(isComplete({ field: "size", op: "gt", value: 0 })).toBe(true);
  });

  it("reads accounts, event types and depth from the tree", () => {
    const tree = {
      match: "all" as const,
      conditions: [
        { field: "account.id", op: "in", value: ["acc_1", "acc_2"] },
        { field: "type", op: "eq", value: "message.received" },
        {
          match: "any" as const,
          conditions: [
            { field: "account.id", op: "eq", value: "acc_1" },
            { match: "not" as const, conditions: [{ field: "type", op: "eq", value: "message.sent" }] },
          ],
        },
      ],
    };
    expect(accountIdsIn(tree)).toEqual(["acc_1", "acc_2"]);
    expect(eventTypesIn(tree)).toEqual(["message.received"]);
    expect(depthOf(tree)).toBe(3);
    expect(isGroup(tree.conditions[2]!)).toBe(true);
    expect(isGroup(tree.conditions[0]!)).toBe(false);
  });

  it("maps the virtual Inbox folder to isInbox() and back", () => {
    expect(folderLeafFor("eq", VIRTUAL_INBOX)).toEqual({ field: "isInbox", op: "eq", value: true });
    expect(folderLeafFor("neq", VIRTUAL_INBOX)).toEqual({ field: "isInbox", op: "eq", value: false });
    expect(folderLeafFor("startsWith", VIRTUAL_INBOX)).toEqual({ field: "folder", op: "startsWith", value: "INBOX" });
    expect(folderLeafFor("eq", "Archive")).toEqual({ field: "folder", op: "eq", value: "Archive" });
    expect(folderViewOf({ field: "isInbox", op: "eq", value: false })).toEqual({ op: "neq", value: VIRTUAL_INBOX });
    expect(folderViewOf({ field: "folder", op: "eq", value: "Sent" })).toEqual({ op: "eq", value: "Sent" });
  });

  it("gives stable keys to tree nodes", () => {
    const a = {};
    const b = {};
    expect(keyOf(a)).toBe(keyOf(a));
    expect(keyOf(a)).not.toBe(keyOf(b));
  });
});

describe("map editor ↔ code round trip", () => {
  it("parses the LegacyApp inbound map into a tree and back to the same text", () => {
    const parsed = parseMap(LEGACY_APP_MAP);
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(parsed.node).toEqual(keyedInboundMap("webhookKey"));
    expect(toCel(parsed.node)).toBe(LEGACY_APP_MAP);
    expect(isVisual(parsed.node)).toBe(true);
  });

  it("is stable for every snippet and starting point", () => {
    const sources = [
      ...SNIPPETS.filter((s) => s.target === "map").map((s) => s.code),
      ...(["event", "data", "slim", "empty"] as const).map((s) => toCel(startingMap(s))),
      '{"a": [1, 2.5, "x", null, true], "b": {"c": [{"d": from()}]}, "e": \'single\'}',
      '{"k": {"x": 1}.x, "l": [1, 2][0]}',
    ];
    for (const src of sources) {
      const first = parseMap(src);
      expect(first.ok, src).toBe(true);
      if (!first.ok) continue;
      const text = toCel(first.node);
      const second = parseMap(text);
      expect(second.ok).toBe(true);
      if (second.ok) {
        expect(second.node).toEqual(first.node);
        expect(toCel(second.node)).toBe(text);
      }
    }
  });

  it("classifies leaf values", () => {
    expect(classifyLeaf("env.webhookKey")).toEqual({ kind: "secret", name: "webhookKey" });
    expect(classifyLeaf('env["other_key"]')).toEqual({ kind: "secret", name: "other_key" });
    expect(classifyLeaf("data.subject")).toEqual({ kind: "field", path: "data.subject" });
    expect(classifyLeaf("type")).toEqual({ kind: "field", path: "type" });
    expect(classifyLeaf("from()")).toEqual({ kind: "helper", call: "from" });
    expect(classifyLeaf('header("x")')).toEqual({ kind: "expr", source: 'header("x")' });
    expect(classifyLeaf('"a" + "b"')).toEqual({ kind: "expr", source: '"a" + "b"' });
    expect(classifyLeaf("-3.5")).toEqual({ kind: "constant", value: -3.5 });
    expect(classifyLeaf('"say \\"hi\\""')).toEqual({ kind: "constant", value: 'say "hi"' });
    expect(classifyLeaf("'it''s'").kind).toBe("expr");
  });

  it("keeps a whole-expression map as one expression node", () => {
    const parsed = parseMap('merge(event, {"vip": inList("vip", fromDomain())})');
    expect(parsed.ok && parsed.node?.kind).toBe("expr");
    if (parsed.ok) expect(isVisual(parsed.node)).toBe(false);
    expect(parseMap("")).toEqual({ ok: true, node: null });
  });

  it("refuses what the tree cannot hold", () => {
    expect(parseMap('{"a": 1 // why\n}')).toEqual({ ok: false, reason: "comment" });
    expect(parseMap('{key: 1}').ok).toBe(false);
    expect(parseMap('{"a" 1}').ok).toBe(false);
    expect(parseMap('{"a": 1').ok).toBe(false);
    expect(parseMap('{"a": "unterminated}').ok).toBe(false);
  });

  it("escapes keys and strings when generating", () => {
    const node: MapNode = {
      kind: "object",
      entries: [{ key: 'we"ird', node: { kind: "constant", value: "line\nbreak" } }],
    };
    const text = toCel(node);
    expect(text).toBe('{\n  "we\\"ird": "line\\nbreak"\n}');
    const back = parseMap(text);
    expect(back.ok && back.node).toEqual(node);
  });

  it("helps editing: kinds, fresh keys, secrets, duplicates", () => {
    expect(nodeOfKind("expr", { kind: "field", path: "data.subject" })).toEqual({ kind: "expr", source: "data.subject" });
    expect(nodeOfKind("constant")).toEqual({ kind: "constant", value: "" });
    expect(freshKey([{ key: "field", node: { kind: "constant", value: 1 } }])).toBe("field2");
    expect(secretsIn(keyedInboundMap("k1"))).toEqual(["k1"]);
    expect(secretsIn({ kind: "expr", source: "env.a + env.b" })).toEqual(["a", "b"]);
    expect(duplicateKeys([
      { key: "a", node: { kind: "constant", value: 1 } },
      { key: "a", node: { kind: "constant", value: 2 } },
    ])).toEqual(["a"]);
  });
});

describe("code autocomplete providers", () => {
  const ctx = { eventTypes: ["message.received"], secretNames: ["webhookKey"], listNames: ["vip"] };

  it("completes payload fields from the schemas", () => {
    const res = completeCel('type == "message.received" && data.mail', ctx);
    expect(res?.options.map((o) => o.label)).toContain("mailbox");
    const nested = completeCel("data.mailbox.", ctx);
    expect(nested?.options.map((o) => o.label)).toEqual(["mailboxId", "path", "specialUse"]);
    expect(nested?.from).toBe("data.mailbox.".length);
    expect(completeCel("event.ti", ctx)?.options.map((o) => o.label)).toContain("time");
    expect(completeCel("account.", ctx)?.options.map((o) => o.label)).toContain("externalId");
  });

  it("completes env secret names, list names and event types", () => {
    expect(completeCel("env.", ctx)?.options.map((o) => o.label)).toEqual(["webhookKey"]);
    expect(completeCel('inList("v', ctx)?.options.map((o) => o.label)).toEqual(["vip"]);
    const types = completeCel('type == "message.', ctx);
    expect(types?.options.map((o) => o.label)).toContain("message.sent");
    expect(types?.from).toBe('type == "'.length);
    expect(completeCel('type in ["message.sent", "pec.', ctx)?.options.length).toBe(EVENT_TYPES.length);
  });

  it("completes helpers with signatures and docs", () => {
    const res = completeCel("isIn", { ...ctx, describe: (k) => (k === "rules.helper.isInbox" ? "Inbox doc" : k) });
    const isInbox = res?.options.find((o) => o.label === "isInbox");
    expect(isInbox?.detail).toBe("isInbox() → bool");
    expect(isInbox?.info).toBe("Inbox doc");
    expect(completeCel("ev", ctx)?.options[0]?.label).toBe("event");
    expect(completeCel('subject() == "dat', ctx)).toBeNull();
    expect(completeCel("", ctx)).toBeNull();
    expect(completeCel("foo.", ctx)).toBeNull();
  });

  it("expands insert templates", () => {
    expect(applyTemplate('inFolder("|")')).toEqual({ text: 'inFolder("")', cursor: 10 });
    expect(applyTemplate("isInbox()")).toEqual({ text: "isInbox()", cursor: 9 });
  });
});

describe("code helpers", () => {
  it("formats filters and maps", () => {
    expect(formatCel('type == "a" && (x || y) || isInbox()', "filter")).toBe('type == "a"\n&& (x || y)\n|| isInbox()');
    expect(formatCel('{"a":1,"b":{"c":data.subject}}', "map")).toBe('{\n  "a": 1,\n  "b": {\n    "c": data.subject\n  }\n}');
    expect(formatCel("  ", "filter")).toBe("");
    expect(splitTopLevel('a && "x && y"').parts).toEqual(["a", '"x && y"']);
  });

  it("finds error positions", () => {
    expect(offsetOf("ab\ncd", 2, 2)).toBe(4);
    expect(offsetOf("ab", 9, 9)).toBe(2);
    expect(positionInMessage("ERROR: <input>:3:7: undeclared")).toEqual({ line: 3, column: 7 });
    expect(positionInMessage("nothing")).toBeNull();
  });

  it("knows the payload a rule makes the sync fetch", () => {
    expect(payloadNeeds('textContains("x")', "")).toEqual(["text"]);
    expect(payloadNeeds("hasAttachments()")).toEqual(["attachments"]);
    expect(payloadNeeds('{"h": data.headers}')).toEqual(["headers"]);
    expect(payloadNeeds('data.headers["x"]')).toEqual([]);
    expect(payloadNeeds("isInbox()")).toEqual([]);
  });
});

describe("presets", () => {
  it("LegacyApp inbound is exactly the production filter + map with the secret NAME", () => {
    const p = presetById("keyedInbound")!;
    expect(p.needsSecret).toBe(true);
    const r = p.apply(DEFAULT_SECRET_NAME);
    expect(normalizeForCompile(r.builder!)).toEqual({
      match: "all",
      conditions: [
        { field: "type", op: "eq", value: "message.received" },
        { field: "isInbox", op: "eq", value: true },
      ],
    });
    expect(toCel(r.map!)).toBe(LEGACY_APP_MAP);
    expect(r.events).toEqual(["message.received"]);
    expect(JSON.stringify(r)).not.toMatch(/"value":"(?!message)/); // never a secret value
  });

  it("LegacyApp send status sends the whole event with the key", () => {
    const r = presetById("keyedStatus")!.apply("statusKey");
    expect(r.events).toEqual(["message.sent", "message.failed", "message.delivery_attempt_failed"]);
    expect(toCel(r.map!)).toBe('{\n  "key": env.statusKey,\n  "body": event\n}');
    expect(normalizeForCompile(r.builder!).conditions[0]).toEqual({
      field: "type",
      op: "in",
      value: ["message.sent", "message.failed", "message.delivery_attempt_failed"],
    });
  });

  it("every preset produces something", () => {
    for (const p of PRESETS) {
      const r = p.apply("k");
      expect(r.builder || r.map, p.id).toBeTruthy();
    }
  });
});
