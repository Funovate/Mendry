import { describe, expect, it } from "vitest";
import { buildSourceConfig, buildTriggerConfig, compatibleGitSecretId, composeGitSecretReplacement, composeHttpsGitCredentialValue, composeSshPrivateKeyValue, filterGitSecrets } from "../src/features/configuration/configuration";

const input = {
  endpoint: " https://mcp.internal/mcp ",
  mcpTransport: "streamable_http",
  mcpHeaders: "{\"X-Tenant\":\"payments\"}",
  evidenceProfile: " errors-context ",
  queryScope: " project ",
  host: " host.internal ",
  port: 22,
  user: " collector ",
  projectFolder: " /srv/app ",
  logPath: " /var/log/app.log ",
  readMode: "tail",
  cloudProvider: " tencent-cls ",
  cloudRegion: " ap-guangzhou ",
  resource: " server-log ",
};

describe("configuration projections", () => {
  it("preserves the complete MCP contract", () => {
    expect(buildSourceConfig("mcp", input)).toEqual({
      schemaVersion: 1,
      endpoint: "https://mcp.internal/mcp",
      transport: "streamable_http",
      headers: { "X-Tenant": "payments" },
      evidenceProfile: "errors-context",
      queryScope: "project",
    });
  });

  it("rejects secret-like non-string header values", () => {
    expect(() => buildSourceConfig("mcp", { ...input, mcpHeaders: "{\"Authorization\":123}" })).toThrow("string values");
  });

  it("builds source-specific and trigger-specific payloads", () => {
    expect(buildSourceConfig("ssh", input)).toEqual(expect.objectContaining({ schemaVersion: 1, host: "host.internal", port: 22, mode: "tail" }));
    expect(buildSourceConfig("cloud", input)).toEqual({ schemaVersion: 1, provider: "tencent-cls", region: "ap-guangzhou", resource: "server-log" });
    expect(buildTriggerConfig("signed_webhook", { eventTypes: "alarm-fired, alarm-recovered", deduplicationKey: " fingerprint ", groupingWindowSeconds: 900, matchExpression: "" })).toEqual({ schemaVersion: 1, eventTypes: ["alarm-fired", "alarm-recovered"], deduplicationKey: "fingerprint" });
  });
});

const secrets = [
  { id: "secret-git", name: "git-http-prod", kind: "git_credential" as const, keyVersion: 1, version: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
  { id: "secret-ssh", name: "git-ssh-prod", kind: "ssh_private_key" as const, keyVersion: 1, version: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
  { id: "secret-pass", name: "git-ssh-pass", kind: "ssh_password" as const, keyVersion: 1, version: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
];

describe("git credential composition", () => {
  it("keeps HTTPS tokens optional-username shaped", () => {
    expect(composeHttpsGitCredentialValue(" deploy ", " token ")).toBe("deploy:token");
    expect(composeHttpsGitCredentialValue(" ", " token ")).toBe("token");
  });

  it("appends an SSH passphrase only when provided", () => {
    expect(composeSshPrivateKeyValue("-----BEGIN KEY-----\nline\n", "")).toBe("-----BEGIN KEY-----\nline");
    expect(composeSshPrivateKeyValue("-----BEGIN KEY-----\nline\n", " phrase ")).toBe("-----BEGIN KEY-----\nline\nphrase");
  });

  it("filters and clears incompatible Git secrets by transport", () => {
    expect(filterGitSecrets(secrets, "https").map((secret) => secret.id)).toEqual(["secret-git"]);
    expect(filterGitSecrets(secrets, "ssh").map((secret) => secret.id)).toEqual(["secret-ssh", "secret-pass"]);
    expect(compatibleGitSecretId("secret-git", secrets, "https")).toBe("secret-git");
    expect(compatibleGitSecretId("secret-git", secrets, "ssh")).toBe("");
  });

  it("omits replacement material only when every sensitive field is empty", () => {
    const empty = { username: "", secret: "", privateKey: "", passphrase: "", sshPassword: "" };
    expect(composeGitSecretReplacement("git_credential", empty)).toEqual({ complete: true });
    expect(composeGitSecretReplacement("ssh_private_key", empty)).toEqual({ complete: true });
    expect(composeGitSecretReplacement("ssh_password", empty)).toEqual({ complete: true });
    expect(composeGitSecretReplacement("git_credential", { ...empty, username: "deploy", secret: "token" })).toEqual({
      complete: true,
      value: "deploy:token",
    });
  });

  it("treats a partial Git rotation draft as incomplete instead of name-only", () => {
    const empty = { username: "", secret: "", privateKey: "", passphrase: "", sshPassword: "" };
    expect(composeGitSecretReplacement("git_credential", { ...empty, username: "deploy" })).toEqual({ complete: false });
    expect(composeGitSecretReplacement("ssh_password", { ...empty, username: "deploy" })).toEqual({ complete: false });
    expect(composeGitSecretReplacement("ssh_private_key", { ...empty, passphrase: "phrase" })).toEqual({ complete: false });
  });
});
