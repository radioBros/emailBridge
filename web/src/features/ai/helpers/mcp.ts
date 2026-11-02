/** MCP client configurations shown in the MCP server panel (the API key stays a placeholder). */

export type McpClientId = "claudeCode" | "claudeDesktop" | "cursor" | "vscode";

export const MCP_CLIENTS: McpClientId[] = ["claudeCode", "claudeDesktop", "cursor", "vscode"];

export const API_KEY_PLACEHOLDER = "<API key>";

/** The config file (or command) each client reads; shown above the snippet. */
export const MCP_CLIENT_FILES: Record<McpClientId, string> = {
  claudeCode: "terminal",
  claudeDesktop: "claude_desktop_config.json",
  cursor: "~/.cursor/mcp.json",
  vscode: ".vscode/mcp.json",
};

const json = (v: unknown): string => JSON.stringify(v, null, 2);

/** Ready-to-paste configuration for one client. */
export const mcpClientConfig = (client: McpClientId, url: string): string => {
  const auth = `Bearer ${API_KEY_PLACEHOLDER}`;
  switch (client) {
    case "claudeCode":
      return `claude mcp add --transport http emailbridge ${url} \\n  --header "Authorization: ${auth}"`;
    case "claudeDesktop":
      // Claude Desktop starts local servers only; mcp-remote bridges it to the HTTP endpoint.
      return json({
        mcpServers: {
          emailbridge: {
            command: "npx",
            args: ["-y", "mcp-remote", url, "--header", `Authorization: ${auth}`],
          },
        },
      });
    case "cursor":
      return json({ mcpServers: { emailbridge: { url, headers: { Authorization: auth } } } });
    case "vscode":
      return json({ servers: { emailbridge: { type: "http", url, headers: { Authorization: auth } } } });
  }
};

/** Host part of a bound address ("[::]:8090" → "", "127.0.0.1:8090" → "127.0.0.1"). */
const hostOf = (addr: string): string => {
  const i = addr.lastIndexOf(":");
  const host = i >= 0 ? addr.slice(0, i) : "";
  return host.replace(/^\[|\]$/g, "");
};

/** Port part of an address (":8090" → "8090"). */
export const portOf = (addr: string): string => {
  const i = addr.lastIndexOf(":");
  return i >= 0 ? addr.slice(i + 1) : addr;
};

const WILDCARD = new Set(["", "0.0.0.0", "::"]);

/**
 * URL of the dedicated listener as a client reaches it: the bound host when it is a specific one,
 * otherwise the host the admin is served from. Plain http — TLS belongs to the reverse proxy.
 */
export const dedicatedUrl = (activeAddr: string, path: string, pageHost: string): string => {
  const bound = hostOf(activeAddr);
  let host = WILDCARD.has(bound) ? pageHost : bound;
  if (host.includes(":") && !host.startsWith("[")) host = `[${host}]`;
  return `http://${host}:${portOf(activeAddr)}${path}`;
};

/** URL on the main listener: from EB_PUBLIC_URL when the server knows it, else the admin's origin. */
export const mainUrl = (serverUrl: string | undefined, path: string, origin: string): string =>
  serverUrl || `${origin.replace(/\/$/, "")}${path}`;
