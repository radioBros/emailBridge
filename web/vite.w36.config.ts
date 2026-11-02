import { defineConfig, mergeConfig } from "vite";
import base from "./vite.config";

export default mergeConfig(
  base,
  defineConfig({
    cacheDir: "node_modules/.vite-w36",
    server: { port: 5196, strictPort: true },
  }),
);
