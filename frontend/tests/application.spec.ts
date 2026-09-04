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
      kind: "mcp",
      credentialSecretId: null,
      config: { schemaVersion: 1, endpoint: "https://mcp.internal/mcp", transport: "streamable_http", headers: {}, evidenceProfile: "errors-context", queryScope: "project" },
      capabilities: ["pull_collection", "context_collection"],
      enabled: true,
      version: 1,
    },
    trigger: {
      id: "trigger-id",
      kind: "custom_rule",
      signingSecretId: null,
      inboundUrl: null,
      config: { schemaVersion: 1, groupingWindowSeconds: 900, matchExpression: "level=ERROR" },
      enabled: true,
      version: 1,
    },
    llm: {
      id: "llm-id",
      provider: "openai",
      baseUrl: "https://api.openai.com",
      credentialSecretId: "secret-source",
      model: "gpt-5.6",
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
    lifecycleGeneration: 1,
    source: "mcp",
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

type RemediationMode = "ready" | "retryable" | "missing" | "started" | "conflict" | "continued" | "blocked";

type MockOptions = {
  authenticated?: boolean;
  role?: MockRole;
  systemRole?: "admin" | "viewer";
  projects?: ReturnType<typeof project>[];
  configured?: boolean;
  failResource?: "audit" | "observations";
  expireResource?: "observations";
  environmentName?: string;
  remediationMode?: RemediationMode;
};

async function mockApi(page: Page, options: MockOptions = {}) {
  let authenticated = options.authenticated ?? true;
  const activeRole = options.role ?? "admin";
  const systemRole = options.systemRole ?? (activeRole === "admin" ? "admin" : "viewer");
  let projects = options.projects ?? [project(activeRole)];
  let currentConfiguration = options.configured === false ? null : configuration({ environmentName: options.environmentName });
  let remediationMode = options.remediationMode ?? "ready";
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
  const configurationDraft = () => ({
    environment: currentConfiguration?.environment ?? null,
    repository: currentConfiguration?.repository ?? null,
    source: currentConfiguration?.source ?? null,
    trigger: currentConfiguration?.trigger ?? null,
    llm: currentConfiguration?.llm ?? null,
  });

  const remediationReview = () => {
    const retryable = remediationMode === "retryable" || remediationMode === "conflict";
    const blocked = remediationMode === "blocked";
    const continued = remediationMode === "continued";
    const started = remediationMode === "started";
    const currentAttempt = continued ? 2 : 1;
    const currentRunId = continued ? "run-2" : "run-1";
    const currentStatus = continued || started ? "queued" : blocked ? "blocked_manual_review" : retryable ? "failed" : "diagnosis_ready_for_review";
    const currentVersion = continued ? 1 : started ? 1 : retryable ? 4 : 3;
    const rootAttempt = {
      id: "run-1",
      attemptNumber: 1,
      status: blocked ? "blocked_manual_review" : retryable ? "failed" : started || continued ? "queued" : "diagnosis_ready_for_review",
      origin: "automatic",
      contextVersion: 1,
      terminalReason: blocked ? "insufficient_evidence" : retryable ? "provider_timeout" : "",
      retryable,
      version: retryable ? 4 : started ? 1 : 3,
      createdAt: now,
      updatedAt: now,
    };
    const attempts = continued ? [rootAttempt, {
      id: "run-2",
      attemptNumber: 2,
      status: "queued",
      origin: "manual_continue",
      contextVersion: 1,
      terminalReason: "",
      retryable: false,
      version: 1,
      createdAt: now,
      updatedAt: now,
    }] : [rootAttempt];
    return {
      runId: currentRunId,
      seriesId: "series-1",
      status: currentStatus,
      generation: 1,
      deployedCommit: "4f9c2b7",
      attemptNumber: currentAttempt,
      version: currentVersion,
      origin: continued ? "manual_continue" : "automatic",
      terminalReason: continued ? "" : blocked ? "insufficient_evidence" : retryable ? "provider_timeout" : "",
      manualSuggestion: blocked ? "Collect runtime logs around the alert window and review the provider path before applying a change." : "",
      retryable,
      continuationAvailable: (retryable || blocked) && !continued,
      attempts,
      diagnosis: {
        fixability: blocked ? "insufficient_evidence" : "code_fixable",
        confidence: 0.9,
        causalReasoning: "The locale reaches validator lookup before the configured fallback is applied.",
        evidenceRefs: ["observation-id"],
        contradictions: [],
        missingEvidence: blocked ? ["runtime logs", "provider detail"] : [],
        recommendedNextAction: blocked ? "Collect runtime logs around the alert window and review the provider path before applying a change." : "Review the guarded fallback patch.",
      },
      plans: [{
        planId: "plan-1",
        intendedBehavior: "Apply the locale fallback before validator lookup",
        risk: "ordinary",
        rationale: "Preserves configured locale behavior while preventing the missing validator lookup.",
        evidenceRefs: ["observation-id"],
        affectedFiles: ["backend/http_encoder.go"],
        rollbackStrategy: "Revert the guarded fallback commit.",
        recommended: true,
      }],
      suggestedDiff: "diff --git a/backend/http_encoder.go b/backend/http_encoder.go",
      risk: "ordinary",
    };
  };

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
    if (path === "/api/v1/projects/real-estate/configuration/draft" && method === "GET") return json(configurationDraft());
    if (path === "/api/v1/projects/real-estate/configuration/source/ssh/containers" && method === "POST") {
      writes.push({ method, path, body });
      return json({ containers: [
        { name: "checkout-api", id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", image: "registry.example/checkout:v1", state: "running", status: "Up 2 minutes" },
        { name: "checkout-api-old", id: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", image: "registry.example/checkout:v0", state: "stopped", status: "Exited (0)" },
      ] });
    }
    if (path.startsWith("/api/v1/projects/real-estate/configuration/") && method === "PUT") {
      const component = path.split("/").at(-1);
      const base = currentConfiguration ?? configuration();
      if (component === "environment") {
        const input = body as Partial<typeof base.environment>;
        currentConfiguration = { ...base, environment: { ...base.environment, ...input, version: base.environment.version + 1 } };
        writes.push({ method, path, body });
        return json(currentConfiguration.environment);
      }
      if (component === "repository") {
        const input = body as Partial<typeof base.repository>;
        currentConfiguration = { ...base, repository: { ...base.repository, ...input, version: base.repository.version + 1 } };
        writes.push({ method, path, body });
        return json(currentConfiguration.repository);
      }
      if (component === "source") {
        const input = body as Partial<typeof base.source>;
        currentConfiguration = { ...base, source: { ...base.source, ...input, version: base.source.version + 1 } };
        writes.push({ method, path, body });
        return json(currentConfiguration.source);
      }
      if (component === "trigger") {
        const input = body as Partial<typeof base.trigger>;
        const inboundUrl = input.kind === "signed_webhook"
          ? base.trigger.inboundUrl ?? "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO"
          : null;
        currentConfiguration = { ...base, trigger: { ...base.trigger, ...input, inboundUrl, version: base.trigger.version + 1 } };
        writes.push({ method, path, body });
        return json(currentConfiguration.trigger);
      }
      if (component === "llm") {
        const input = body as Partial<NonNullable<typeof base.llm>>;
        currentConfiguration = { ...base, llm: { ...base.llm, ...input, version: (base.llm?.version ?? 0) + 1 } };
        writes.push({ method, path, body });
        return json(currentConfiguration.llm);
      }
    }
    if (path === "/api/v1/projects/real-estate/configuration" && method === "PUT") {
      currentConfiguration = body as ReturnType<typeof configuration>;
      if (currentConfiguration.trigger.kind === "signed_webhook" && !currentConfiguration.trigger.inboundUrl) {
        currentConfiguration = {
          ...currentConfiguration,
          trigger: { ...currentConfiguration.trigger, inboundUrl: "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO" },
        };
      }
      writes.push({ method, path, body });
      return json(currentConfiguration);
    }
    if (path === "/api/v1/projects/real-estate/configuration/webhook-token" && method === "POST") {
      if (currentConfiguration?.trigger.kind !== "signed_webhook") {
        return error(400, "invalid_request", "Project request is invalid.");
      }
      const inboundUrl = "http://127.0.0.1:8080/hooks/zyxwvutsrqponmlkjihgfedcbaZYXWVUTSRQPONML";
      currentConfiguration = { ...currentConfiguration, trigger: { ...currentConfiguration.trigger, inboundUrl } };
      writes.push({ method, path, body });
      return json({ inboundUrl });
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
    if (path === "/api/v1/projects/real-estate/incidents/INC-2048/remediation/start" && method === "POST") {
      if (remediationMode !== "missing") return error(409, "remediation_conflict", "Remediation already exists.");
      writes.push({ method, path, body });
      remediationMode = "started";
      return json({ runId: "run-1", seriesId: "series-1", status: "queued", generation: 1, attemptNumber: 1, version: 1 });
    }
    if (path === "/api/v1/projects/real-estate/incidents/INC-2048/remediation/retry" && method === "POST") {
      writes.push({ method, path, body });
      if (remediationMode === "conflict") return error(409, "remediation_conflict", "Remediation request conflicts with the current incident.");
      if (remediationMode !== "retryable") return error(409, "remediation_unsupported", "The current remediation result cannot be continued.");
      remediationMode = "continued";
      return json({ runId: "run-2", seriesId: "series-1", status: "queued", generation: 1, attemptNumber: 2, version: 1 });
    }
    if (path === "/api/v1/projects/real-estate/incidents/INC-2048/remediation" && method === "GET") {
      if (remediationMode === "missing") return error(404, "remediation_not_found", "Remediation run was not found.");
      return json(remediationReview());
    }
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
    if (path === "/api/v1/projects/real-estate/llm/models" && method === "POST") {
      writes.push({ method, path, body });
      return json({ models: ["gpt-4.1", "gpt-5.6"] });
    }
    if (path === "/api/v1/projects/real-estate/llm/chat" && method === "POST") {
      writes.push({ method, path, body });
      return json({ status: "ok" });
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
  const expectedTimestamp = await page.evaluate((value) => new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(value)), "2026-08-13T07:59:00Z");
  await expect(page.locator("time")).toHaveText(expectedTimestamp);
  await expect(page.locator("time")).toHaveAttribute("datetime", "2026-08-13T07:59:00Z");

  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByText("https://git.example.internal/platform/real-estate-api.git", { exact: true })).toBeVisible();
  await expect(page.getByText("production@4f9c2b7", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Members" }).click();
  await expect(page.getByRole("row", { name: /operator operator Yes Yes No/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /viewer viewer Yes No No/ })).toBeVisible();

  await page.getByRole("link", { name: "Audit" }).click();
  await expect(page.getByText("Project configuration updated.", { exact: true })).toBeVisible();
});

test("keeps incident detail heading levels visually consistent", async ({ page }) => {
  await mockApi(page, { role: "admin" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  const sectionHeadings = ["Occurrence summary", "Lifecycle", "Remediation review"];
  const subsectionHeadings = ["Run overview", "Diagnosis", "Attempts", "Plans", "Suggested diff"];
  const fontSizes = async (names: string[]) => Promise.all(names.map((name) => page.getByRole("heading", { name }).evaluate((element) => getComputedStyle(element).fontSize)));

  expect(new Set(await fontSizes(sectionHeadings))).toEqual(new Set(["16px"]));
  expect(new Set(await fontSizes(subsectionHeadings))).toEqual(new Set(["15px"]));
});

test("renders remediation diagnostics in the selected incident detail", async ({ page }) => {
  await mockApi(page, { role: "admin" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  await expect(page.getByRole("heading", { name: "Remediation review" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Diagnosis" })).toBeVisible();
  await expect(page.getByText("The locale reaches validator lookup before the configured fallback is applied.", { exact: true })).toBeVisible();
  await expect(page.getByText("Evidence observation-id", { exact: true })).toBeVisible();
  await expect(page.getByText("Apply the locale fallback before validator lookup", { exact: true })).toBeVisible();
  await expect(page.getByText("diff --git a/backend/http_encoder.go b/backend/http_encoder.go", { exact: true })).toBeVisible();
});

test("highlights the manual fix suggestion for blocked remediation", async ({ page }) => {
  await mockApi(page, { role: "operator", remediationMode: "blocked" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  await expect(page.getByRole("heading", { name: "人工修复建议 / Manual fix suggestion", exact: true })).toBeVisible();
  await expect(page.getByText("Collect runtime logs around the alert window and review the provider path before applying a change.", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "缺失证据", exact: true })).toBeVisible();
  await expect(page.getByText("runtime logs", { exact: true })).toBeVisible();
  await expect(page.getByText("provider detail", { exact: true })).toBeVisible();
});

test("operator can continue a retryable remediation and see attempt two", async ({ page }) => {
  const state = await mockApi(page, { role: "operator", remediationMode: "retryable" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  await expect(page.getByRole("button", { name: "Continue analysis", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Continue analysis", exact: true }).click();
  await expect(page.getByText("Continuation queued. Refreshing the latest attempt.", { exact: true })).toBeVisible();
  await expect(page.getByText("Attempt 2", { exact: true })).toBeVisible();
  await expect(page.getByText("manual_continue · queued", { exact: true })).toBeVisible();
  await expect(page.locator("body")).not.toContainText("sk-");
  expect(state.writes).toContainEqual({
    method: "POST",
    path: "/api/v1/projects/real-estate/incidents/INC-2048/remediation/retry",
    body: { generation: 1, runId: "run-1", version: 4 },
  });
});

test("viewer sees retry history but no continuation control", async ({ page }) => {
  await mockApi(page, { role: "viewer", remediationMode: "retryable" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  await expect(page.getByRole("button", { name: "Continue analysis", exact: true })).toHaveCount(0);
  await expect(page.getByText("Viewer access is read-only.", { exact: true }).first()).toBeVisible();
});

test("a stale remediation retry reports conflict without rendering a new attempt", async ({ page }) => {
  const state = await mockApi(page, { role: "operator", remediationMode: "conflict" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  await page.getByRole("button", { name: "Continue analysis", exact: true }).click();
  await expect(page.getByText("Remediation request conflicts with the current incident.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue analysis", exact: true })).toBeDisabled();
  await expect(page.getByText("Attempt 2", { exact: true })).toHaveCount(0);
  expect(state.writes.filter((write) => write.path.endsWith("/remediation/retry"))).toHaveLength(1);
});

test("operator can start remediation when the incident has no series", async ({ page }) => {
  const state = await mockApi(page, { role: "operator", remediationMode: "missing" });
  await page.goto("/projects/real-estate/incidents/INC-2048");

  await expect(page.getByRole("button", { name: "Start remediation", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Start remediation", exact: true }).click();
  await expect(page.getByText("Attempt 1", { exact: true })).toBeVisible();
  expect(state.writes).toContainEqual({
    method: "POST",
    path: "/api/v1/projects/real-estate/incidents/INC-2048/remediation/start",
    body: { generation: 1 },
  });
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
  await expect(page.getByLabel("Inbound webhook URL")).toBeVisible();
  await expect(page.getByRole("button", { name: "Generate URL" })).toBeDisabled();
  await expect(page.getByText("Save the signed webhook configuration first. The first save creates the inbound URL.", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Webhook signing credential")).toHaveCount(0);
  await expect(page.getByLabel("Webhook event types")).toHaveCount(0);
  await page.getByRole("button", { name: "Save trigger" }).click();
  await expect(page.getByLabel("Inbound webhook URL")).toHaveValue("http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO");
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
  await page.getByLabel("SSH private key", { exact: true }).fill("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----");
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
  await page.getByRole("button", { name: "Save Git repository" }).click();

  await page.getByRole("tab", { name: "LLM provider" }).click();
  await expect(page.getByLabel("LLM base URL")).toHaveValue("https://api.openai.com");
  await page.getByRole("button", { name: "New credential" }).click();
  await page.getByLabel("LLM credential name").fill("openai-prod");
  await page.getByLabel("LLM credential value").fill("sk-e2e-openai-key");
  await page.getByRole("button", { name: "Store llm credential" }).click();
  await expect(page.getByLabel("API key credential")).toHaveValue("secret-openai-prod");
  await expect(page.getByText("sk-e2e-openai-key", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Load models" }).click();
  await page.getByLabel("LLM model").selectOption("gpt-5.6");
  await expect(page.getByLabel("LLM model")).toHaveValue("gpt-5.6");
  await page.getByRole("button", { name: "Test with hi" }).click();
  await expect(page.getByText("Chat probe succeeded.", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Save LLM provider" }).click();
  await expect(page.getByLabel("LLM model")).toHaveValue("gpt-5.6");

  await page.getByRole("tab", { name: "Trigger" }).click();
  await expect(page.getByLabel("Inbound webhook URL")).toHaveValue("http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO");
  await page.getByRole("button", { name: "Regenerate URL" }).click();
  await expect(page.getByLabel("Inbound webhook URL")).toHaveValue("http://127.0.0.1:8080/hooks/zyxwvutsrqponmlkjihgfedcbaZYXWVUTSRQPONML");

  await page.getByRole("link", { name: "Members" }).click();
  await page.getByLabel("Member username").fill("oncall");
  await page.getByLabel("Member role").selectOption("operator");
  await page.getByRole("button", { name: "Add or update" }).click();
  await expect(page.getByRole("cell", { name: "oncall" })).toBeVisible();

  expect(state.writes).toEqual(expect.arrayContaining([
    { method: "PATCH", path: "/api/v1/projects/real-estate/incidents/INC-2048/status", body: { status: "Recovered" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "git-http-ci", kind: "git_credential", value: "deploy:https-token-value" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "git-ssh-ci", kind: "ssh_private_key", value: "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----\nkey-passphrase" } },
    { method: "PUT", path: "/api/v1/projects/real-estate/members/oncall", body: { role: "operator" } },
    { method: "POST", path: "/api/v1/projects/real-estate/repository/refs", body: { remoteUrl: "https://git.example.internal/platform/real-estate-api.git", transport: "https", credentialSecretId: "secret-git" } },
    { method: "PUT", path: "/api/v1/projects/real-estate/configuration/trigger", body: { kind: "signed_webhook", signingSecretId: null, config: { schemaVersion: 2, provider: "generic", eventTypes: ["alarm"], deduplicationKey: "title" }, enabled: true } },
    { method: "PUT", path: "/api/v1/projects/real-estate/configuration/repository", body: { remoteUrl: "https://git.example.internal/platform/real-estate-api.git", scmProvider: "yunxiao", transport: "https", credentialSecretId: "secret-git", productionBranch: "production", deployedCommit: "abcdef0123456789abcdef0123456789abcdef01" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "openai-prod", kind: "http_bearer", value: "sk-e2e-openai-key" } },
    { method: "POST", path: "/api/v1/projects/real-estate/llm/models", body: { baseUrl: "https://api.openai.com", credentialSecretId: "secret-openai-prod" } },
    { method: "POST", path: "/api/v1/projects/real-estate/llm/chat", body: { baseUrl: "https://api.openai.com", credentialSecretId: "secret-openai-prod", model: "gpt-5.6" } },
    { method: "PUT", path: "/api/v1/projects/real-estate/configuration/llm", body: { provider: "openai", baseUrl: "https://api.openai.com", credentialSecretId: "secret-openai-prod", model: "gpt-5.6" } },
    { method: "POST", path: "/api/v1/projects/real-estate/configuration/webhook-token", body: {} },
  ]));
  expect(state.getConfiguration()?.repository.productionBranch).toBe("production");
  expect(state.getConfiguration()?.repository.deployedCommit).toBe("abcdef0123456789abcdef0123456789abcdef01");
  expect(state.getConfiguration()?.source.config).toEqual({ schemaVersion: 1, endpoint: "https://mcp.internal/mcp", transport: "streamable_http", headers: {}, evidenceProfile: "errors-context", queryScope: "project" });
  const triggerWrite = state.writes.find((write) => write.path.endsWith("/configuration/trigger"));
  expect(triggerWrite?.body).not.toHaveProperty("llm");
  expect(triggerWrite?.body).not.toHaveProperty(["trigger", "name"]);
});

const gitImportedPem = "-----BEGIN OPENSSH PRIVATE KEY-----\nfile-imported-secret-key\n-----END OPENSSH PRIVATE KEY-----";
const sourceImportedPem = "-----BEGIN OPENSSH PRIVATE KEY-----\nsource-imported-secret-key\n-----END OPENSSH PRIVATE KEY-----";

test("imports an SSH PEM file on Git and source credentials and rejects non-key files", async ({ page }) => {
  const state = await mockApi(page, { role: "admin" });
  await page.goto("/projects/real-estate/configuration/edit");

  await page.getByRole("button", { name: "New credential" }).click();
  await expect(page.getByLabel("SSH private key file", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Cancel new credential" }).click();

  await page.getByLabel("Git transport").selectOption("ssh");
  await page.getByRole("button", { name: "New credential" }).click();
  await page.getByLabel("Git credential name").fill("git-ssh-pem");
  await page.getByLabel("SSH private key file", { exact: true }).setInputFiles({
    name: "deploy.pem",
    mimeType: "text/plain",
    buffer: Buffer.from(gitImportedPem),
  });
  await expect(page.getByLabel("SSH private key", { exact: true })).toHaveValue(gitImportedPem);
  await expect(page.getByText("deploy.pem", { exact: true })).toBeVisible();
  await page.getByLabel("SSH passphrase").fill("imported-passphrase");
  await page.getByRole("button", { name: "Store git credential" }).click();
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git-ssh-pem");
  await expect(page.getByText("file-imported-secret-key", { exact: true })).toHaveCount(0);
  await expect(page.getByText("imported-passphrase", { exact: true })).toHaveCount(0);
  await expect(page.getByText("deploy.pem", { exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: "Edit credential" }).click();
  await expect(page.getByLabel("Git credential type")).toHaveValue("ssh_private_key");
  await expect(page.getByLabel("Replacement SSH private key", { exact: true })).toHaveValue("");
  const gitReplacementPem = "-----BEGIN OPENSSH PRIVATE KEY-----\nfile-replaced-secret-key\n-----END OPENSSH PRIVATE KEY-----";
  await page.getByLabel("Replacement SSH private key file").setInputFiles({
    name: "deploy-rotated.pem",
    mimeType: "text/plain",
    buffer: Buffer.from(gitReplacementPem),
  });
  await expect(page.getByLabel("Replacement SSH private key", { exact: true })).toHaveValue(gitReplacementPem);
  await page.getByRole("button", { name: "Save git credential" }).click();
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git-ssh-pem");
  await expect(page.getByText("file-replaced-secret-key", { exact: true })).toHaveCount(0);
  await expect(page.getByText("deploy-rotated.pem", { exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: "Edit credential" }).click();
  await page.getByLabel("Git credential name").fill("git-ssh-pem-renamed");
  await expect(page.getByLabel("Replacement SSH private key", { exact: true })).toHaveValue("");
  await page.getByRole("button", { name: "Save git credential" }).click();
  await expect(page.getByLabel("Git credential reference")).toHaveValue("secret-git-ssh-pem");
  await expect(page.getByRole("option", { name: "git-ssh-pem-renamed · ssh_private_key" })).toHaveCount(1);

  await page.getByRole("button", { name: "New credential" }).click();
  await page.getByLabel("Git credential name").fill("git-ssh-rejected");
  await page.getByLabel("SSH private key file", { exact: true }).setInputFiles({
    name: "id_rsa.ppk",
    mimeType: "text/plain",
    buffer: Buffer.from("PuTTY-User-Key-File-2: ssh-rsa\nPrivate-Lines: 1\n"),
  });
  await expect(page.getByText("The selected content is not a PEM or OpenSSH private key.", { exact: true })).toBeVisible();
  await expect(page.getByLabel("SSH private key", { exact: true })).toHaveValue("");
  await expect(page.getByRole("button", { name: "Store git credential" })).toBeDisabled();
  await expect(page.getByText("PuTTY-User-Key-File-2", { exact: true })).toHaveCount(0);
  await page.getByLabel("SSH private key", { exact: true }).fill("not-a-private-key");
  await page.getByRole("button", { name: "Store git credential" }).click();
  await expect(page.getByText("The selected content is not a PEM or OpenSSH private key.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel new credential" }).click();

  await page.getByRole("tab", { name: "Collection source" }).click();
  await page.getByLabel("Source type").selectOption("ssh");
  await page.getByRole("button", { name: "New credential" }).click();
  await expect(page.getByLabel("Source credential type")).toHaveValue("ssh_private_key");
  await expect(page.getByLabel("Source SSH private key file")).toBeVisible();
  await page.getByLabel("Source credential name").fill("source-ssh-pem");
  await page.getByLabel("Source SSH private key file").setInputFiles({
    name: "collector.pem",
    mimeType: "text/plain",
    buffer: Buffer.from(sourceImportedPem),
  });
  await expect(page.getByLabel("Source credential value")).toHaveValue(sourceImportedPem);
  await page.getByRole("button", { name: "Store source credential" }).click();
  await expect(page.getByLabel("Source credential reference")).toHaveValue("secret-source-ssh-pem");
  await expect(page.getByText("source-imported-secret-key", { exact: true })).toHaveCount(0);
  await expect(page.getByText("collector.pem", { exact: true })).toHaveCount(0);

  await page.getByRole("tab", { name: "Trigger" }).click();
  await page.getByLabel("Trigger type").selectOption("signed_webhook");
  await expect(page.getByLabel("Inbound webhook URL")).toBeVisible();
  await expect(page.getByRole("button", { name: "Generate URL" })).toBeDisabled();
  await expect(page.getByLabel("Webhook credential value")).toHaveCount(0);
  await expect(page.getByLabel("Webhook SSH private key file")).toHaveCount(0);
  await expect(page.getByLabel("SSH private key file", { exact: true })).toHaveCount(0);

  expect(state.writes).toEqual(expect.arrayContaining([
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "git-ssh-pem", kind: "ssh_private_key", value: `${gitImportedPem}\nimported-passphrase` } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-git-ssh-pem", body: { name: "git-ssh-pem", value: gitReplacementPem } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-git-ssh-pem", body: { name: "git-ssh-pem-renamed" } },
    { method: "POST", path: "/api/v1/projects/real-estate/secrets", body: { name: "source-ssh-pem", kind: "ssh_private_key", value: sourceImportedPem } },
  ]));
  expect(state.writes.some((write) => write.method === "POST" && write.path.endsWith("/secrets") && JSON.stringify(write.body).includes("git-ssh-rejected"))).toBeFalsy();
  expect(state.writes.some((write) => write.method === "POST" && write.path.endsWith("/secrets") && JSON.stringify(write.body).includes("not-a-private-key"))).toBeFalsy();
  expect(state.writes.some((write) => JSON.stringify(write.body).includes("PuTTY-User-Key-File"))).toBeFalsy();
});

test("administrator renames a project and refreshes a derived environment name", async ({ page }) => {
  const state = await mockApi(page, { role: "admin", environmentName: "Real Estate API" });
  await page.goto("/projects/real-estate/incidents");
  await expect(page.getByRole("button", { name: "Project Real Estate API" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Edit project name" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Project name" })).toHaveCount(0);

  await page.getByRole("button", { name: "Edit project name" }).click();
  await page.getByRole("textbox", { name: "Project name" }).fill("Property Platform");
  await page.getByRole("button", { name: "Save project name" }).click();

  await expect(page.getByRole("button", { name: "Project Property Platform" })).toBeVisible();
  await expect(page.getByText("Property Platform", { exact: true }).first()).toBeVisible();
  await expect(page).toHaveURL(/\/projects\/real-estate\/incidents$/);
  expect(state.writes).toContainEqual({ method: "PATCH", path: "/api/v1/projects/real-estate", body: { name: "Property Platform" } });

  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByText("Property Platform / Property Platform")).toBeVisible();
  await expect(page.getByRole("cell", { name: "Property Platform" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Project identity" })).toHaveCount(0);
  await expect(page.getByLabel("Project key")).toHaveCount(0);
});

test("administrator preserves a distinct environment name when renaming a project", async ({ page }) => {
  const state = await mockApi(page, { role: "admin", environmentName: "Production" });
  await page.goto("/projects/real-estate/configuration");
  await page.getByRole("button", { name: "Edit project name" }).click();
  await page.getByRole("textbox", { name: "Project name" }).fill("Property Platform");
  await page.getByRole("button", { name: "Save project name" }).click();
  await expect(page.getByRole("button", { name: "Project Property Platform" })).toBeVisible();
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
  await expect(page.getByLabel("Inbound webhook URL")).toBeVisible();
  await expect(page.getByRole("button", { name: "Generate URL" })).toBeDisabled();
  await expect(page.getByText("Save the signed webhook configuration first. The first save creates the inbound URL.", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Webhook signing credential")).toHaveCount(0);

  expect(state.writes).toEqual(expect.arrayContaining([
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-git", body: { name: "git-http-renamed" } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-git", body: { name: "git-http-rotated", value: "deploy:rotated-https-token" } },
    { method: "PATCH", path: "/api/v1/projects/real-estate/secrets/secret-source", body: { name: "source-bearer-renamed", value: "rotated-source-token" } },
  ]));
  expect(state.writes.some((write) => JSON.stringify(write.body).includes("ciphertext"))).toBeFalsy();
});

test("administrator discovers a Docker container through the bounded source probe", async ({ page }) => {
  const state = await mockApi(page, { role: "admin" });
  await page.goto("/projects/real-estate/configuration/edit");
  await page.getByRole("tab", { name: "Collection source" }).click();
  await page.getByLabel("Source type").selectOption("ssh");
  await page.getByLabel("Source credential reference").selectOption("secret-ssh");
  await page.getByLabel("SSH deployment").selectOption("docker");
  await expect(page.getByLabel("Docker container")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Docker container" })).toHaveCount(0);

  await page.getByRole("button", { name: "Refresh containers" }).click();
  await expect(page.getByRole("option", { name: /checkout-api · registry\.example\/checkout:v1/ })).toHaveCount(1);
  await page.getByLabel("Docker container").selectOption("checkout-api");
  await expect(page.getByLabel("Docker container")).toHaveValue("checkout-api");

  await page.getByLabel("SSH host").fill("new-host.example.internal");
  await expect(page.getByLabel("Docker container")).toHaveValue("");
  await expect(page.getByRole("option", { name: /checkout-api · registry\.example\/checkout:v1/ })).toHaveCount(0);

  await page.getByRole("button", { name: "Refresh containers" }).click();
  await page.getByLabel("Docker container").selectOption("checkout-api");
  await page.getByRole("button", { name: "Save collection source" }).click();

  const sourceWrite = state.writes.find((write) => write.method === "PUT" && write.path.endsWith("/configuration/source"));
  expect(sourceWrite?.body).toEqual(expect.objectContaining({
    kind: "ssh",
    credentialSecretId: "secret-ssh",
    config: expect.objectContaining({ deployment: { kind: "docker", containerName: "checkout-api" } }),
  }));
  await expect(page.locator("body")).not.toContainText("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
});

test("viewer receives the permission matrix without mutation controls", async ({ page }) => {
  const state = await mockApi(page, { role: "viewer", systemRole: "viewer" });
  await page.goto("/");
  await expect(page.getByText("viewer · local user", { exact: true })).toBeVisible();
  await page.getByText("Validator locale fr is not registered", { exact: true }).click();
  await expect(page.getByText("Viewer access is read-only.", { exact: true }).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Recovered" })).toBeDisabled();

  await page.getByRole("link", { name: "Configuration" }).click();
  await expect(page.getByRole("button", { name: "Edit configuration" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Project identity" })).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Project name" })).toHaveCount(0);
  await expect(page.getByLabel("Project key")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Edit project name" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Project Real Estate API" })).toBeVisible();
  await expect(page.getByText("Inbound webhook", { exact: true })).toHaveCount(0);
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

  test("remediation review stays ordered within the viewport", async ({ page }) => {
    await mockApi(page, { role: "admin" });
    await page.goto("/projects/real-estate/incidents/INC-2048");

    const diagnosis = page.getByRole("heading", { name: "Diagnosis" });
    const attempts = page.getByRole("heading", { name: "Attempts" });
    const plans = page.getByRole("heading", { name: "Plans" });
    await expect(diagnosis).toBeVisible();
    await expect(attempts).toBeVisible();
    await expect(plans).toBeVisible();

    const positions = await Promise.all([diagnosis, attempts, plans].map(async (heading) => (await heading.boundingBox())?.y ?? 0));
    expect(positions[0]).toBeLessThan(positions[1]);
    expect(positions[1]).toBeLessThan(positions[2]);
    await plans.scrollIntoViewIfNeeded();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  });

  test("project navigation and event stream stay within the viewport", async ({ page }) => {
    await mockApi(page, { role: "operator", systemRole: "viewer" });
    await page.goto("/");
    await page.getByLabel("Open navigation").click();
    await page.getByRole("link", { name: "Event stream" }).click();
    await expect(page.getByRole("heading", { name: "Event stream" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  });
});
