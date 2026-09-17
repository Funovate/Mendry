import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export const apiProxy = {
  target: "http://127.0.0.1:8080",
} as const;

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": apiProxy,
    },
  },
});
