import { api } from "@/api/client";

/** local until schema regen */
export interface McpToolset {
  id: string;
  tools: number;
}

/** GET/PUT /v1/mcp/server. local until schema regen */
export interface McpServerStatus {
  listen: string;
  listening: boolean;
  activeAddr?: string;
  error?: string;
  mainPath: string;
  dedicatedPath: string;
  mainUrl?: string;
  canChange: boolean;
  toolsets: McpToolset[];
}

export const getMcpServer = async (): Promise<McpServerStatus> =>
  api.get<McpServerStatus>("/mcp/server");

/** Sets the dedicated listener ("" turns it off); instance owners only. */
export const putMcpServer = async (listen: string): Promise<McpServerStatus> =>
  api.put<McpServerStatus>("/mcp/server", { listen });
