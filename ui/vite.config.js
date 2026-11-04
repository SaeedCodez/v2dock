import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The built UI goes to ../internal/web/dist, which the Go gateway embeds
// (`//go:embed all:dist`). The Docker build rebuilds it from source.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "./src") },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, "../internal/web/dist"),
    emptyOutDir: true,
    chunkSizeWarningLimit: 800,
  },
  server: {
    // `npm run dev` proxies the API to a running gateway (docker-compose.local.yml).
    proxy: {
      "/api": { target: "http://127.0.0.1:8088", changeOrigin: false },
    },
  },
});
