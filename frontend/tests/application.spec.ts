import { expect, test, type Page } from "@playwright/test";

type MockRole = "admin" | "operator" | "viewer";

const now = "2026-08-13T08:00:00Z";

function project(role: MockRole, overrides: Partial<{ id: string; key: string; name: string; description: string }> = {}) {
  return {
    id: "0198-project",
    key: "real-estate",
    name: "Real Estate API",
    description: "Production property service",
    ...overrides,
    role,
    capabilities: {
      read: true,
      writeIncidents: role !== "viewer",
      manageMembers: role === "admin",
      manageConfiguration: role === "admin",
    },
    version: 1,
    createdAt: now,
    updatedAt: now,
  };
}

function configuration(overrides: { environmentName?: string } = {}) {
  return {
    environment: { id: "env-id", key: "production", name: overrides.environmentName ?? "Production", service: "real-estate-backend", version: 1 },
    repository: {
      id: "repo-id",
      remoteUrl: "https://git.example.internal/platform/real-estate-api.git",
      scmProvider: "yunxiao",
      transport: "https",
      credentialSecretId: "secret-git",
      productionBranch: "production",
      deployedCommit: "4f9c2b7",
      version: 1,
    },
    source: {
      id: "source-id",
      name: "cls-production",
      kind: "mcp",
      credentialSecretId: null,
      config: { schemaVersion: 1, endpoint: "https://mcp.internal/mcp", transport: "streamable_http", headers: {}, evidenceProfile: "errors-context", queryScope: "project" },
      capabilities: ["pull_collection", "context_collection"],
      enabled: true,
      version: 1,
    },
    trigger: {
      id: "trigger-id",
      name: "backend-errors",
      kind: "custom_rule",
      signingSecretId: null,
      config: { schemaVersion: 1, groupingWindowSeconds: 900, matchExpression: "level=ERROR" },
      enabled: true,
      version: 1,
    },
  };
}

function incident(overrides: Partial<ReturnType<typeof baseIncident>> = {}) {
  return { ...baseIncident(), ...overrides };
}

function baseIncident() {
  return {
    id: "INC-2048",
    title: "Validator locale fr is not registered",
    fingerprint: "b2a8:validator-locale",
    status: "Open",
    priority: "Info",
    source: "cls-production",
    sourceId: "source-id",
    environmentId: "env-id",
    firstSeen: "2026-08-13T07:40:00Z",
    lastSeen: "2026-08-13T07:58:00Z",
    occurrenceCount: 47,
    hostCount: 2,
    muted: false,
    notificationSummary: "Lifecycle default",
    version: 1,
    createdAt: now,
    updatedAt: now,
  };
}

type MockOptions = {
  authenticated?: boolean;
  role?: MockRole;
  systemRole?: "admin" | "viewer";
  projects?: ReturnType<typeof project>[];
  configured?: boolean;
  failResource?: "audit" | "observations";
  expireResource?: "observations";
  environmentName?: string;
};

async function mockApi(page: Page, options: MockOptions = {}) {
  let authenticated = options.authenticated ?? true;
  const activeRole = options.role ?? "admin";
  const systemRole = options.systemRole ?? (activeRole === "admin" ? "admin" : "viewer");
  let projects = options.projects ?? [project(activeRole)];
  let currentConfiguration = options.configured === false ? null : configuration({ environmentName: options.environmentName });
  let incidents = [incident()];
  let members = [
    { userId: "user-admin", username: "admin", role: "admin", version: 1, createdAt: now, updatedAt: now },
    { userId: "user-operator", username: "operator", role: "operator", version: 1, createdAt: now, updatedAt: now },
    { userId: "user-viewer", username: "viewer", role: "viewer", version: 1, createdAt: now, updatedAt: now },
  ];
  let secrets = [
    { id: "secret-git", name: "git-http-prod", kind: "git_credential", keyVersion: 1, version: 1, createdAt: now, updatedAt: now },
    { id: "secret-source", name: "source-bearer-prod", kind: "http_bearer", keyVersion: 1, version: 1, createdAt: now, updatedAt: now },
    { id: "secret-webhook", name: "webhook-hmac-prod", kind: "webhook_hmac", keyVersion: 1, version: 1, createdAt: now, updatedAt: now },
    { id: "secret-ssh", name: "git-ssh-prod", kind: "ssh_private_key", keyVersion: 1, version: 1, createdAt: now, updatedAt: now },
  ];
  const writes: Array<{ method: string; path: string; body: unknown }> = [];

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    const body = request.postDataJSON?.() ?? undefined;

    const json = (value: unknown, status = 200) => {
      const isList = Array.isArray(value);
      return route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify({
          code: "ok",
          message: "OK",
          data: value,
          meta: { requestId: "test-request", durationMs: 1, ...(isList ? { total: value.length } : {}) },
        }),
      });
    };
    const error = (status: number, code: string, message: string) => route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify({ error: { code, message, requestId: "test-request" } }),
    });

    if (path === "/api/v1/auth/me" && method === "GET") {
      return authenticated ? json({ id: "current-user", username: activeRole, role: systemRole }) : error(401, "unauthenticated", "Authentication is required.");
    }
    if (path === "/api/v1/auth/login" && method === "POST") {
      authenticated = true;
      writes.push({ method, path, body });
      return json({ id: "current-user", username: activeRole, role: systemRole });
    }
    if (path === "/api/v1/auth/logout" && method === "POST") {
      authenticated = false;
      return route.fulfill({ status: 204 });
    }
    if (!authenticated) return error(401, "unauthenticated", "Authentication is required.");

    if (path === "/api/v1/projects" && method === "GET") return json(projects);
    if (path === "/api/v1/projects" && method === "POST") {
      const input = body as { key: string; name: string; description: string };
      const created = { ...project("admin"), id: "new-project", ...input };
      projects = [...projects, created];
      writes.push({ method, path, body });
      return json(created, 201);
    }
    if (path === "/api/v1/projects/real-estate" && method === "PATCH") {
      const input = body as { name: string };
      const current = projects.find((item) => item.key === "real-estate");
      if (!current) return error(404, "not_found", "Project was not found.");
      const updated = { ...current, name: input.name, version: current.version + 1 };
      projects = projects.map((item) => item.key === "real-estate" ? updated : item);
      if (currentConfiguration && currentConfiguration.environment.name === current.name) {
        currentConfiguration = {
          ...currentConfiguration,
          environment: { ...currentConfiguration.environment, name: input.name },
        };
      }
      writes.push({ method, path, body });
      return json(updated);
    }

    if (path === "/api/v1/projects/real-estate/configuration" && method === "GET") {
      return currentConfiguration ? json(currentConfiguration) : error(404, "configuration_not_found", "Project configuration was not found.");
    }
    if (path === "/api/v1/projects/real-estate/configuration" && method === "PUT") {
      currentConfiguration = body as ReturnType<typeof configuration>;
      writes.push({ method, path, body });
      return json(currentConfiguration);
    }
    if (path === "/api/v1/projects/real-estate/observations" && method === "GET") {
      if (options.expireResource === "observations") {
        authenticated = false;
        return error(401, "unauthenticated", "Authentication is required.");
      }
      if (options.failResource === "observations") return error(503, "source_unavailable", "The observation stream is unavailable.");
      return json([{
        id: "observation-id",
        environmentId: "env-id",
        sourceId: "source-id",
        service: "real-estate-backend",
        occurredAt: "2026-08-13T07:59:00Z",
        level: "ERROR",
        message: "validator instance for language fr not registered",
        host: "api-prod-01",
        requestId: null,
        fingerprint: "b2a8:validator-locale",
        attributes: {},
        ingestedAt: now,
      }]);
    }
    if (path === "/api/v1/projects/real-estate/incidents" && method === "GET") return json(incidents);
    if (path === "/api/v1/projects/payments/incidents" && method === "GET") {
      return json([incident({ id: "INC-3001", title: "Payment capture timed out", fingerprint: "payments:capture-timeout" })]);
    }
    if (path === "/api/v1/projects/real-estate/incidents/INC-2048/status" && method === "PATCH") {
      const status = (body as { status: string }).status;
      incidents = [{ ...incidents[0], status, version: incidents[0].version + 1 }];
      writes.push({ method, path, body });
      return json(incidents[0]);
    }
    if (path === "/api/v1/projects/real-estate/audit-events" && method === "GET") {
      if (options.failResource === "audit") return error(503, "audit_unavailable", "Audit history is unavailable.");
      return json([{ id: "audit-id", actorUserId: "user-admin", action: "configuration.updated", targetType: "project", targetId: "0198-project", summary: "Project configuration updated.", metadata: {}, occurredAt: now }]);
    }
    if (path === "/api/v1/projects/real-estate/members" && method === "GET") return json(members);
    if (path.startsWith("/api/v1/projects/real-estate/members/") && method === "PUT") {
      const username = decodeURIComponent(path.split("/").at(-1) ?? "");
      const member = { userId: `user-${username}`, username, role: (body as { role: MockRole }).role, version: 1, createdAt: now, updatedAt: now };
      members = [...members.filter((item) => item.username !== username), member];
      writes.push({ method, path, body });
      return json(member);
    }
    if (path.startsWith("/api/v1/projects/real-estate/members/") && method === "DELETE") {
      const username = decodeURIComponent(path.split("/").at(-1) ?? "");
      members = members.filter((item) => item.username !== username);
      writes.push({ method, path, body });
      return route.fulfill({ status: 204 });
    }
    if (path === "/api/v1/projects/real-estate/secrets" && method === "GET") return json(secrets);
    if (path === "/api/v1/projects/real-estate/secrets" && method === "POST") {
      const input = body as { name: string; kind: string; value: string };
      const created = { id: `secret-${input.name}`, name: input.name, kind: input.kind, keyVersion: 1, version: 1, createdAt: now, updatedAt: now };
      secrets = [...secrets, created];
      writes.push({ method, path, body });
      return json(created, 201);
    }
    if (path.startsWith("/api/v1/projects/real-estate/secrets/") && method === "PATCH") {
      const secretId = decodeURIComponent(path.split("/").at(-1) ?? "");
      const input = body as { name: string; value?: string };
      if (Object.hasOwn(input, "value") && String(input.value ?? "").trim() === "") {
        return error(400, "invalid_request", "The request is invalid.");
      }
      const existing = secrets.find((item) => item.id === secretId);
      if (!existing) return error(404, "not_found", "Project credential was not found.");
      const updated = {
        ...existing,
        name: input.name,
        version: existing.version + 1,
        keyVersion: input.value ? existing.keyVersion + 1 : existing.keyVersion,
      };
      secrets = secrets.map((item) => item.id === secretId ? updated : item);
      writes.push({ method, path, body });
      return json(updated);
    }
    if (path === "/api/v1/projects/real-estate/repository/refs" && method === "POST") {
      writes.push({ method, path, body });
      return json({
        defaultBranch: "main",
        deployedCommit: "0123456789abcdef0123456789abcdef01234567",
        branches: [
          { name: "main", commit: "0123456789abcdef0123456789abcdef01234567" },
          { name: "production", commit: "abcdef0123456789abcdef0123456789abcdef01" },
        ],
      });
    }

    return error(404, "not_found", `No mock for ${method} ${path}`);
  });

  return { writes, getConfiguration: () => currentConfiguration };
}

test("requires a server session and logs in through the real auth route", async ({ page }) => {
  const state = await mockApi(page, { authenticated: false, role: "operator", systemRole: "viewer" });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await page.getByLabel("Username").fill("operator");
  await page.getByLabel("Password").fill("correct-password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: /Incidents/ })).toBeVisible();
  expect(state.writes).toContainEqual({ method: "POST", path: "/api/v1/auth/login", body: { username: "operator", password: "correct-password" } });
});

test("loads project-owned configuration, events, incidents, members, and audit records", async ({ page }) => {
  await mockApi(page, { role: "admin" });
  await page.goto("/");
  await expect(page.getByText("Validator locale fr is not registered", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Event stream" }).click();
  await expect(page.getByText("validator instance for language fr not registered", { exact: false })).toBeVisible();
  await expect(page.getByText("real-estate-backend", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByText("https://git.example.internal/platform/real-estate-api.git", { exact: true })).toBeVisible();
  await expect(page.getByText("production@4f9c2b7", { exact: true })).toBeVisible();
  await expect(page.getByText("cls-production", { exact: true })).toBeVisible();
  await expect(page.getByText("backend-errors", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Members" }).click();
  await expect(page.getByRole("row", { name: /operator operator Yes Yes No/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /viewer viewer Yes No No/ })).toBeVisible();

  await page.getByRole("link", { name: "Audit" }).click();
  await expect(page.getByText("Project configuration updated.", { exact: true })).toBeVisible();
});

test("distinguishes an existing project with no configuration from an empty project list", async ({ page }) => {
  await mockApi(page, { role: "admin", configured: false });
  await page.goto("/");
  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByRole("heading", { name: "Configuration required" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Configure project" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No accessible projects" })).toHaveCount(0);
});

test("administrator persists credentials, configuration, members, and incident lifecycle", async ({ page }) => {
  const state = await mockApi(page, { role: "admin" });
  await page.goto("/");

  await page.getByText("Validator locale fr is not registered", { exact: true }).click();
  await page.getByRole("button", { name: "Recovered", exact: true }).click();
  await expect(page.getByRole("button", { name: "Recovered", exact: true })).toBeDisabled();

  await page.getByRole("link", { name: "Configuration" }).click();
  await page.getByRole("button", { name: "Edit configuration" }).click();
  await expect(page.getByLabel("Git remote URL")).toBeVisible();

  await page.getByRole("tab", { name: "Trigger" }).click();
  await expect(page.getByLabel("Git remote URL")).toHaveCount(0);
  await page.getByLabel("Trigger type").selectOption("signed_webhook");
  await page.getByRole("button", { name: "New credential" }).click();
  await page.getByLabel("Webhook credential name").fill("webhook-signing-prod");
  await page.getByLabel("Webhook credential type").selectOption("webhook_hmac");
  await page.getByLabel("Webhook credential value").fill("super-secret-hmac");
  await page.getByRole("button", { name: "Store webhook credential" }).click();
  await expect(page.getByLabel("Webhook signing credential")).toHaveValue("secret-webhook-signing-prod");
  await expect(page.getByText("super-secret-hmac", { exact: true })).toHaveCount(0);

  await page.getByRole("tab", { name: "Git repository" }).click();
  await page.getByRole("button", { name: "New credential" }).click();
  await page.getByLabel("Git credential name").fill("git-http-ci");
  await page.getByLabel("Git username").fill("deploy");
  await page.getByLabel("Git HTTP password or token").fill("https-token-value");
  await page.getByRole("button", { name: "Store git credential" }).click();
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git-http-ci");
  await expect(page.getByText("https-token-value", { exact: true })).toHaveCount(0);

  await page.getByLabel("Git transport").selectOption("ssh");
  await expect(page.getByLabel("Git credential reference")).toHaveValue("");
  await page.getByRole("button", { name: "New credential" }).click();
  await page.getByLabel("Git credential name").fill("git-ssh-ci");
  await page.getByLabel("SSH private key").fill("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----");
  await page.getByLabel("SSH passphrase").fill("key-passphrase");
  await page.getByRole("button", { name: "Store git credential" }).click();
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git-ssh-ci");
  await expect(page.getByText("secret-key", { exact: true })).toHaveCount(0);
  await expect(page.getByText("key-passphrase", { exact: true })).toHaveCount(0);

  await page.getByLabel("Git transport").selectOption("https");
  await page.getByLabel("Git credential reference").selectOption("secret-git");
  await page.getByRole("button", { name: "Read from remote" }).click();
  await expect(page.getByLabel("Production branch")).toHaveValue("main");
  await expect(page.getByLabel("Deployed commit")).toHaveValue("0123456789abcdef0123456789abcdef01234567");
  await page.getByLabel("Production branch").selectOption("production");
  await expect(page.getByLabel("Deployed commit")).toHaveValue("abcdef0123456789abcdef0123456789abcdef01");
  await page.getByRole("button", { name: "Save configuration" }).click();
  await expect(page.getByText("Configuration saved.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Save configuration" })).toBeVisible();
  await expect(page.getByLabel("Git remote URL")).toBeVisible();

  await page.getByRole("link", { name: "Members" }).click();
  await page.getByLabel("Member username").fill("oncall");
  await page.getByLabel("Member role").selectOption("operator");
  await page.getByRole("button", { name: "Add or update" }).click();
  await expect(page.getByRole("cell", { name: "oncall" })).toBeVisible();

  expect(state.writes).toEqual(expect.arrayContaining([
    { method: "PATCH", path: "/api/v1/projects/real-estate/incidents/INC-2048/status", body: { status: "Recovered" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "webhook-signing-prod", kind: "webhook_hmac", value: "super-secret-hmac" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "git-http-ci", kind: "git_credential", value: "deploy:https-token-value" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "git-ssh-ci", kind: "ssh_private_key", value: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----\nkey-passphrase" } },
    { method: "PUT", path: "/api/v1/projects/real-estate/members/oncall", body: { role: "operator" } },
    { method: "POST", path: "/api/v1/projects/real-estate/repository/refs", body: { remoteUrl: "https://git.example.internal/platform/real-estate-api.git", transport: "https", credentialSecretId: "secret-git" } },
  ]));
  expect(state.writes.some((write) => write.method === "PUT" && write.path.endsWith("/configuration"))).toBeTruthy();
  expect(state.getConfiguration()?.repository.productionBranch).toBe("production");
  expect(state.getConfiguration()?.repository.deployedCommit).toBe("abcdef0123456789abcdef0123456789abcdef01");
  expect(state.getConfiguration()?.source.config).toEqual({ schemaVersion: 1, endpoint: "https://mcp.internal/mcp", transport: "streamable_http", headers: {}, evidenceProfile: "errors-context", queryScope: "project" });
});

test("administrator renames a project and refreshes a derived environment name", async ({ page }) => {
  const state = await mockApi(page, { role: "admin", environmentName: "Real Estate API" });
  await page.goto("/projects/real-estate/configuration");
  await expect(page.getByRole("heading", { name: "Project identity" })).toBeVisible();
  await expect(page.getByLabel("Project key")).toHaveValue("real-estate");
  await expect(page.getByLabel("Project key")).toHaveAttribute("readonly", "");
  await expect(page.getByRole("cell", { name: "Real Estate API" })).toBeVisible();

  await page.getByRole("button", { name: "Edit project name" }).click();
  await page.getByLabel("Project name").fill("Property Platform");
  await page.getByRole("button", { name: "Save project name" }).click();

  await expect(page.getByRole("button", { name: "Project Property Platform" })).toBeVisible();
  await expect(page.getByText("Property Platform / Property Platform")).toBeVisible();
  await expect(page.getByRole("cell", { name: "Property Platform" })).toBeVisible();
  await expect(page).toHaveURL(/\/projects\/real-estate\/configuration$/);
  expect(state.writes).toContainEqual({ method: "PATCH", path: "/api/v1/projects/real-estate", body: { name: "Property Platform" } });
});

test("administrator preserves a distinct environment name when renaming a project", async ({ page }) => {
  const state = await mockApi(page, { role: "admin", environmentName: "Production" });
  await page.goto("/projects/real-estate/configuration");
  await page.getByRole("button", { name: "Edit project name" }).click();
  await page.getByLabel("Project name").fill("Property Platform");
  await page.getByRole("button", { name: "Save project name" }).click();
  await expect(page.getByText("Property Platform / Production")).toBeVisible();
  await expect(page.getByRole("cell", { name: "Production", exact: true })).toBeVisible();
  expect(state.writes).toContainEqual({ method: "PATCH", path: "/api/v1/projects/real-estate", body: { name: "Property Platform" } });
});

test("administrator edits Git, source, and webhook credentials without disclosing secrets", async ({ page }) => {
  const state = await mockApi(page, { role: "admin" });
  await page.goto("/projects/real-estate/configuration/edit");

  await page.getByRole("button", { name: "Edit credential" }).click();
  await expect(page.getByLabel("Git credential type")).toHaveValue("git_credential");
  await expect(page.getByLabel("Git credential type")).toHaveAttribute("readonly", "");
  await expect(page.getByLabel("Replacement Git HTTP password or token")).toHaveValue("");
  await page.getByLabel("Git credential name").fill("git-http-renamed");
  await page.getByRole("button", { name: "Save git credential" }).click();
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git");
  await expect(page.getByRole("option", { name: "git-http-renamed · git_credential" })).toHaveCount(1);

  await page.getByRole("button", { name: "Edit credential" }).click();
  await page.getByLabel("Git credential name").fill("git-http-rotated");
  await page.getByLabel("Git username").fill("deploy");
  await expect(page.getByRole("button", { name: "Save git credential" })).toBeDisabled();
  await page.getByLabel("Replacement Git HTTP password or token").fill("rotated-https-token");
  await page.getByRole("button", { name: "Save git credential" }).click();
  await expect(page.getByText("rotated-https-token", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git");

  await page.getByRole("tab", { name: "Collection source" }).click();
  await page.getByLabel("Source credential reference").selectOption("secret-source");
  await page.getByRole("button", { name: "Edit credential" }).click();
  await expect(page.getByLabel("Source credential type")).toHaveValue("http_bearer");
  await page.getByLabel("Source credential name").fill("source-bearer-renamed");
  await page.getByLabel("Source replacement value").fill("rotated-source-token");
  await page.getByRole("button", { name: "Save source credential" }).click();
  await expect(page.getByLabel("Source credential reference")).toHaveValue("secret-source");
  await expect(page.getByText("rotated-source-token", { exact: true })).toHaveCount(0);

  await page.getByRole("tab", { name: "Trigger" }).click();
  await page.getByLabel("Trigger type").selectOption("signed_webhook");
  await page.getByLabel("Webhook signing credential").selectOption("secret-webhook");
  await page.getByRole("button", { name: "Edit credential" }).click();
  await expect(page.getByLabel("Webhook credential type")).toHaveValue("webhook_hmac");
  await page.getByLabel("Webhook credential name").fill("webhook-hmac-renamed");
  await page.getByRole("button", { name: "Save webhook credential" }).click();
  await expect(page.getByLabel("Webhook signing credential")).toHaveValue("secret-webhook");

  expect(state.writes).toEqual(expect.arrayContaining([
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-git", body: { name: "git-http-renamed" } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-git", body: { name: "git-http-rotated", value: "deploy:rotated-https-token" } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-source", body: { name: "source-bearer-renamed", value: "rotated-source-token" } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-webhook", body: { name: "webhook-hmac-renamed" } },
  ]));
  expect(state.writes.some((write) => JSON.stringify(write.body).includes("ciphertext"))).toBeFalsy();
});

test("viewer receives the permission matrix without mutation controls", async ({ page }) => {
  const state = await mockApi(page, { role: "viewer", systemRole: "viewer" });
  await page.goto("/");
  await expect(page.getByText("viewer · local user", { exact: true })).toBeVisible();
  await page.getByText("Validator locale fr is not registered", { exact: true }).click();
  await expect(page.getByText("Viewer access is read-only.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Recovered" })).toBeDisabled();

  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByRole("button", { name: "Edit configuration" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Project identity" })).toBeVisible();
  await expect(page.getByLabel("Project name")).toHaveValue("Real Estate API");
  await expect(page.getByLabel("Project name")).toHaveAttribute("readonly", "");
  await expect(page.getByLabel("Project key")).toHaveValue("real-estate");
  await expect(page.getByRole("button", { name: "Edit project name" })).toHaveCount(0);
  await page.getByRole("link", { name: "Members" }).click();
  await expect(page.getByRole("row", { name: /operator operator Yes Yes No/ })).toBeVisible();
  await expect(page.getByLabel("Member username")).toHaveCount(0);
  expect(state.writes).toEqual([]);
});

test("system administrator creates a project when the project list is empty", async ({ page }) => {
  const state = await mockApi(page, { systemRole: "admin", projects: [] });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "No accessible projects" })).toBeVisible();
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page).toHaveURL(/\/projects\/new$/);
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Create project" })).toBeVisible();
  await page.getByLabel("Project name").fill("Payments API");
  await expect(page.getByLabel("Project key")).toHaveValue("payments-api");
  await page.getByLabel("Project description").fill("Payment processing incidents");
  await page.getByRole("button", { name: "Create project" }).click();
  await expect(page.getByRole("button", { name: "Project Payments API" })).toBeVisible();
  expect(state.writes).toContainEqual({ method: "POST", path: "/api/v1/projects", body: { key: "payments-api", name: "Payments API", description: "Payment processing incidents" } });
});

test("supports direct project URLs and browser history", async ({ page }) => {
  await mockApi(page, { role: "admin" });
  await page.goto("/projects/real-estate/audit");
  await expect(page.getByRole("heading", { name: "Audit" })).toBeVisible();
  await expect(page).toHaveURL(/\/projects\/real-estate\/audit$/);

  await page.getByRole("link", { name: "Event stream" }).click();
  await expect(page.getByRole("heading", { name: "Event stream" })).toBeVisible();
  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByRole("heading", { name: "Configuration" })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Event stream" })).toBeVisible();
});

test("keeps project data isolated while switching projects", async ({ page }) => {
  const payments = project("operator", { id: "project-payments", key: "payments", name: "Payments API", description: "Payment processing" });
  await mockApi(page, { role: "admin", projects: [project("admin"), payments] });
  await page.goto("/projects/real-estate/incidents");
  await expect(page.getByText("Validator locale fr is not registered", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Project Real Estate API" }).click();
  await page.getByRole("link", { name: /Payments API/ }).click();
  await expect(page).toHaveURL(/\/projects\/payments\/incidents$/);
  await expect(page.getByText("Payment capture timed out", { exact: true })).toBeVisible();
  await expect(page.getByText("Validator locale fr is not registered", { exact: true })).toHaveCount(0);
});

test("isolates a feature failure and keeps other project routes usable", async ({ page }) => {
  await mockApi(page, { role: "admin", failResource: "audit" });
  await page.goto("/projects/real-estate/audit");
  await expect(page.getByText("Audit history is unavailable.", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Incidents" }).click();
  await expect(page.getByText("Validator locale fr is not registered", { exact: true })).toBeVisible();
});

test("returns to login when a project request loses authentication", async ({ page }) => {
  await mockApi(page, { role: "operator", systemRole: "viewer", expireResource: "observations" });
  await page.goto("/projects/real-estate/incidents");
  await expect(page.getByText("Validator locale fr is not registered", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Event stream" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByText("Validator locale fr is not registered", { exact: true })).toHaveCount(0);
});

test.describe("mobile", () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true });

  test("project navigation and event stream stay within the viewport", async ({ page }) => {
    await mockApi(page, { role: "operator", systemRole: "viewer" });
    await page.goto("/");
    await page.getByLabel("Open navigation").click();
    await page.getByRole("link", { name: "Event stream" }).click();
    await expect(page.getByRole("heading", { name: "Event stream" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  });
});
