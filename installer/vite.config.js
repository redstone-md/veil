import { defineConfig } from "vite";

// Tauri serves the dev server on a fixed port via tauri.conf.json;
// keep this in sync with `build.devUrl` there.
export default defineConfig({
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
  build: {
    target: "es2021",
    minify: "esbuild",
    sourcemap: false,
  },
  envPrefix: ["VITE_", "TAURI_"],
});
