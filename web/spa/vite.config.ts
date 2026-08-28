import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Served by the Go server under /app/. Dev proxies the API to :8080.
export default defineConfig({
  base: "/app/",
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    proxy: { "/api": "http://localhost:8080" },
  },
});
