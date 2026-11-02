import { existsSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export type StackUser = {
  email: string;
  password: string;
  totpSecret: string;
};

export type StackInfo = {
  baseUrl: string;
  owner: StackUser;
  viewer: StackUser;
  ids: Record<string, string>;
};

const stackPath = join(dirname(fileURLToPath(import.meta.url)), "..", ".stack.json");

export const loadStack = (): StackInfo => {
  if (!existsSync(stackPath)) {
    throw new Error(
      `Missing ${stackPath}. Start the stack with: go run ./test/uistack`,
    );
  }
  return JSON.parse(readFileSync(stackPath, "utf8")) as StackInfo;
};
