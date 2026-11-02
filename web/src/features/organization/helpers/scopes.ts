/** API key scopes from S10 §3 (read/write pairs + admin). Granular ids appended in the dialog. */
const BASE_SCOPES = [
  "organizations",
  "members",
  "apikeys",
  "audit",
  "settings",
  "logs",
  "jobs",
  "accounts",
  "outbound",
  "sandbox",
  "webhooks",
  "routes",
  "deliveries",
  "events",
  "templates",
  "gateways",
  "suppression",
  "oauth",
  "exports",
  "integrations",
] as const;

/** Resources that support `<resource>:<mode>:<id>` (P3.4a). */
export const GRANULAR_RESOURCES = ["accounts", "webhooks", "routes"] as const;
export type GranularResource = (typeof GRANULAR_RESOURCES)[number];

/** MCP access for AI assistants (docs/spec/MCP-server.md); the key also needs the scopes its tools use. */
export const MCP_SCOPE = "mcp:use";

export const API_KEY_SCOPES: string[] = [
  "admin",
  MCP_SCOPE,
  ...BASE_SCOPES.flatMap((s) => [`${s}:read`, `${s}:write`]),
];

/** Scope picker items; labelOf gives a friendly title (e.g. "MCP — mcp:use"), default the scope itself. */
export const scopeItems = (
  labelOf?: (scope: string) => string | undefined,
): Array<{ title: string; value: string }> =>
  API_KEY_SCOPES.map((value) => ({ title: labelOf?.(value) ?? value, value }));

export const granularScope = (
  resource: GranularResource,
  mode: "read" | "write",
  id: string,
): string => `${resource}:${mode}:${id}`;

export const parseGranularScopes = (
  scopes: string[],
): Array<{ resource: GranularResource; mode: "read" | "write"; id: string }> => {
  const out: Array<{ resource: GranularResource; mode: "read" | "write"; id: string }> = [];
  for (const s of scopes) {
    const parts = s.split(":");
    if (parts.length !== 3) continue;
    const [resource, mode, id] = parts;
    if (
      (GRANULAR_RESOURCES as readonly string[]).includes(resource) &&
      (mode === "read" || mode === "write") &&
      id
    ) {
      out.push({ resource: resource as GranularResource, mode, id });
    }
  }
  return out;
};

/** Base (non-granular) scopes only. */
export const baseScopesOf = (scopes: string[]): string[] =>
  scopes.filter((s) => s.split(":").length <= 2);
