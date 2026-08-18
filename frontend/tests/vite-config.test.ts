import { describe, expect, it } from "vitest";
import { apiProxy } from "../vite.config";

describe("Vite API proxy", () => {
  it("preserves the browser Host so backend same-origin checks accept writes", () => {
    expect(apiProxy.target).toBe("http://127.0.0.1:8080");
    expect(apiProxy).not.toHaveProperty("changeOrigin");
  });
});
