import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiContractError, ApiError, api } from "../src/api";

const user = { id: "user-1", username: "operator", role: "operator" };
const project = {
  id: "project-1",
  key: "payments",
  name: "Payments",
  description: "Payments project",
  role: "operator",
  capabilities: { read: true, writeIncidents: true, manageMembers: false, manageConfiguration: false },
  version: 1,
  createdAt: "2026-08-13T08:00:00Z",
  updatedAt: "2026-08-13T08:00:00Z",
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("API contract boundary", () => {
  it("validates successful responses and includes credentials", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: user,
      meta: { requestId: "request-1", durationMs: 3 },
    }), { status: 200, headers: { "X-Request-ID": "request-1" } }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.me()).resolves.toEqual(user);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/auth/me", expect.objectContaining({ credentials: "include" }));
  });

  it("unwraps list data while preserving the server total", async () => {
    const projects = [project];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: projects,
      meta: { requestId: "request-2", durationMs: 0, total: 17 },
    }), { status: 200 })));

    await expect(api.listProjects()).resolves.toEqual({ items: projects, total: 17 });
  });

  it("rejects malformed successful payloads", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: { ...user, role: "owner" },
      meta: { requestId: "request-1", durationMs: 1 },
    }), { status: 200 })));
    await expect(api.me()).rejects.toBeInstanceOf(ApiContractError);
  });

  it("rejects a list response without a server total", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok", message: "OK", data: [], meta: { requestId: "request-1", durationMs: 1 },
    }), { status: 200 })));
    await expect(api.listProjects()).rejects.toBeInstanceOf(ApiContractError);
  });

  it("rejects invalid success metadata", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok", message: "OK", data: user, meta: { requestId: "request-1", durationMs: -1 },
    }), { status: 200 })));
    await expect(api.me()).rejects.toBeInstanceOf(ApiContractError);
  });

  it("preserves backend error metadata", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "forbidden", message: "Project access is denied.", requestId: "request-1" },
    }), { status: 403 })));

    const error = await api.listProjects().catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toEqual(expect.objectContaining({
      status: 403,
      code: "forbidden",
      message: "Project access is denied.",
      requestId: "request-1",
    }));
  });

  it("uses a stable fallback for non-JSON errors", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("gateway unavailable", { status: 502 })));
    await expect(api.me()).rejects.toEqual(expect.objectContaining({
      status: 502,
      code: "request_failed",
      message: "Request failed with status 502.",
    }));
  });

  it("accepts 204 for empty mutations", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(api.logout()).resolves.toBeUndefined();
  });

  it("patches a project name on the stable project path", async () => {
    const updated = { ...project, name: "Payments Platform", version: 2 };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: updated,
      meta: { requestId: "request-3", durationMs: 2 },
    }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.updateProjectName("payments", "Payments Platform")).resolves.toEqual(updated);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/payments", expect.objectContaining({
      method: "PATCH",
      body: JSON.stringify({ name: "Payments Platform" }),
    }));
  });

  it("omits value for a name-only secret update and validates the safe DTO", async () => {
    const secret = {
      id: "secret-1",
      name: "git-http-renamed",
      kind: "git_credential",
      keyVersion: 1,
      version: 2,
      createdAt: "2026-08-13T08:00:00Z",
      updatedAt: "2026-08-13T09:00:00Z",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: secret,
      meta: { requestId: "request-4", durationMs: 2 },
    }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.updateSecret("payments", "secret-1", { name: "git-http-renamed" })).resolves.toEqual(secret);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/payments/secrets/secret-1", expect.objectContaining({
      method: "PATCH",
      body: JSON.stringify({ name: "git-http-renamed" }),
    }));
    expect(String(fetchMock.mock.calls[0][1].body)).not.toContain("value");
  });

  it("includes replacement material only when rotating a secret", async () => {
    const secret = {
      id: "secret-1",
      name: "git-http-prod",
      kind: "git_credential",
      keyVersion: 2,
      version: 3,
      createdAt: "2026-08-13T08:00:00Z",
      updatedAt: "2026-08-13T09:00:00Z",
    };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: secret,
      meta: { requestId: "request-5", durationMs: 2 },
    }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(api.updateSecret("payments", "secret-1", {
      name: "git-http-prod",
      value: "replacement-token",
    })).resolves.toEqual(secret);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/payments/secrets/secret-1", expect.objectContaining({
      method: "PATCH",
      body: JSON.stringify({ name: "git-http-prod", value: "replacement-token" }),
    }));
  });

  it("strips unexpected secret material from a successful update response", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: "ok",
      message: "OK",
      data: {
        id: "secret-1",
        name: "git-http-prod",
        kind: "git_credential",
        keyVersion: 1,
        version: 2,
        ciphertext: "opaque",
        nonce: "hidden",
        value: "replacement-token",
        createdAt: "2026-08-13T08:00:00Z",
        updatedAt: "2026-08-13T09:00:00Z",
      },
      meta: { requestId: "request-6", durationMs: 2 },
    }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(api.updateSecret("payments", "secret-1", { name: "git-http-prod" })).resolves.toEqual({
      id: "secret-1",
      name: "git-http-prod",
      kind: "git_credential",
      keyVersion: 1,
      version: 2,
      createdAt: "2026-08-13T08:00:00Z",
      updatedAt: "2026-08-13T09:00:00Z",
    });
  });
});
