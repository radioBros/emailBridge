/** CEL helper functions of the rules engine (W73 §2): signature, return type and an insert template. */
export interface HelperDef {
  name: string;
  /** Shown in the completion list, e.g. `inFolder(pathOrSpecialUse)`. */
  signature: string;
  returns: string;
  /** Text inserted on accept; `|` marks the cursor. */
  insert: string;
}

const h = (name: string, args: string, returns: string, insert?: string): HelperDef => ({
  name,
  signature: `${name}(${args})`,
  returns,
  insert: insert ?? (args ? `${name}(|)` : `${name}()`),
});

export const HELPERS: HelperDef[] = [
  h("isInbox", "", "bool"),
  h("inFolder", "pathOrSpecialUse", "bool", 'inFolder("|")'),
  h("hasLabel", "name", "bool", 'hasLabel("|")'),
  h("labels", "", "list(string)"),
  h("text", "", "string"),
  h("textContains", "s", "bool", 'textContains("|")'),
  h("textMatches", "re2", "bool", 'textMatches("|")'),
  h("subject", "", "string"),
  h("subjectContains", "s", "bool", 'subjectContains("|")'),
  h("subjectMatches", "re2", "bool", 'subjectMatches("|")'),
  h("hasAttachments", "", "bool"),
  h("attachments", "", "list(map)"),
  h("hasAttachment", "globOrRe2", "bool", 'hasAttachment("*.pdf|")'),
  h("attachmentTypes", "", "list(string)"),
  h("from", "", "string"),
  h("fromDomain", "", "string"),
  h("toAddresses", "", "list(string)"),
  h("ccAddresses", "", "list(string)"),
  h("inDomains", "list", "bool", 'inDomains(["|"])'),
  h("replyTo", "", "string"),
  h("isReply", "", "bool"),
  h("isForward", "", "bool"),
  h("isAutoReply", "", "bool"),
  h("isBounce", "", "bool"),
  h("header", "name", "string", 'header("|")'),
  h("hasHeader", "name", "bool", 'hasHeader("|")'),
  h("headerValues", "name", "list(string)", 'headerValues("|")'),
  h("domain", "address", "string"),
  h("spf", "", "string"),
  h("dkim", "", "string"),
  h("dmarc", "", "string"),
  h("spamScore", "", "double"),
  h("pecType", "", "string"),
  h("isPecReceipt", "", "bool"),
  h("localTime", "tz", "timestamp", 'localTime("|")'),
  h("hourOf", "ts", "int"),
  h("weekdayOf", "ts", "int"),
  h("businessHours", "tz, window, days", "bool", 'businessHours("Europe/Rome", "09:00-18:00", "Mon-Fri")|'),
  h("inList", "name, value", "bool", 'inList("|", fromDomain())'),
  h("merge", "a, b", "map", "merge(event, {|})"),
  h("pick", "map, keys", "map", 'pick(data, ["|"])'),
  h("omit", "map, keys", "map", 'omit(event, ["|"])'),
];

/** Top-level variables of every rule. */
export const VARIABLES = [
  { name: "event", detail: "map" },
  { name: "data", detail: "map" },
  { name: "type", detail: "string" },
  { name: "account", detail: "map" },
  /** The account's free-form metadata (G4), always a map; keys with "." or "-" read as account.metadata["crm.id"]. */
  { name: "account.metadata", detail: "map(string, dyn)" },
  { name: "env", detail: "map(string)" },
  { name: "now", detail: "timestamp" },
];

export const KEYWORDS = ["true", "false", "null", "in", "has"];

export const helperKey = (name: string): string => `rules.helper.${name}`;
