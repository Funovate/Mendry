import { describe, expect, it } from "vitest";
import { buildSourceConfig, buildTriggerConfig, compatibleGitSecretId, composeGitSecretReplacement, composeHttpsGitCredentialValue, composeSshPrivateKeyValue, filterGitSecrets, inspectSshPrivateKeyDraft, inspectSshPrivateKeyFile, SSH_PRIVATE_KEY_MAX_BYTES, sshPrivateKeyInspectionMessage } from "../src/features/configuration/configuration";

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
    expect(buildSourceConfig("ssh", input)).toEqual(expect.objectContaining({ schemaVersion: 2, host: "host.internal", port: 22, mode: "tail", deployment: { kind: "host" } }));
    expect(buildSourceConfig("cloud", input)).toEqual({ schemaVersion: 1, provider: "tencent-cls", region: "ap-guangzhou", resource: "server-log" });
    expect(buildTriggerConfig("signed_webhook", { eventTypes: "", deduplicationKey: "", groupingWindowSeconds: 900, matchExpression: "" })).toEqual({ schemaVersion: 2, provider: "generic", eventTypes: ["alarm"], deduplicationKey: "title" });
    expect(buildTriggerConfig("signed_webhook", { eventTypes: "alarm", deduplicationKey: "title", groupingWindowSeconds: 900, matchExpression: "", webhookProvider: "aws_cloudwatch", awsTopicArn: " arn:aws:sns:us-east-1:123456789012:alarms " })).toEqual({
      schemaVersion: 3, provider: "aws_cloudwatch", eventTypes: ["alarm"], deduplicationKey: "alarm_arn",
      awsCloudWatch: { topicArn: "arn:aws:sns:us-east-1:123456789012:alarms" },
    });
    expect(buildSourceConfig("ssh", { ...input, sshDeploymentKind: "docker", sshContainerName: "checkout-api" })).toEqual(expect.objectContaining({
      schemaVersion: 2,
      deployment: { kind: "docker", containerName: "checkout-api" },
    }));
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

const opensshPem = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmU=\n-----END OPENSSH PRIVATE KEY-----";
const rsaPem = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----";
const pkcs8Pem = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSj\n-----END PRIVATE KEY-----";

describe("SSH private key inspection", () => {
  it("accepts OpenSSH, RSA, and PKCS#8 PEM private keys", () => {
    expect(inspectSshPrivateKeyDraft(opensshPem)).toEqual({ ok: true, text: opensshPem });
    expect(inspectSshPrivateKeyDraft(rsaPem)).toEqual({ ok: true, text: rsaPem });
    expect(inspectSshPrivateKeyDraft(pkcs8Pem)).toEqual({ ok: true, text: pkcs8Pem });
  });

  it("rejects empty, public-key, and PuTTY drafts without interpolating contents", () => {
    expect(inspectSshPrivateKeyDraft("   ")).toEqual({ ok: false, reason: "empty" });
    expect(inspectSshPrivateKeyDraft("-----BEGIN OPENSSH PUBLIC KEY-----\nabc\n-----END OPENSSH PUBLIC KEY-----")).toEqual({ ok: false, reason: "not_pem" });
    expect(inspectSshPrivateKeyDraft("PuTTY-User-Key-File-2: ssh-rsa\nPrivate-Lines: 1\n")).toEqual({ ok: false, reason: "not_pem" });
    expect(sshPrivateKeyInspectionMessage("not_pem")).toBe("The selected content is not a PEM or OpenSSH private key.");
    expect(sshPrivateKeyInspectionMessage("empty")).not.toMatch(/BEGIN|PuTTY|abc/);
  });

  it("rejects an oversized key and a Git compose that exceeds the stored-secret limit", () => {
    const oversized = `-----BEGIN OPENSSH PRIVATE KEY-----\n${"a".repeat(SSH_PRIVATE_KEY_MAX_BYTES)}\n-----END OPENSSH PRIVATE KEY-----`;
    expect(inspectSshPrivateKeyDraft(oversized)).toEqual({ ok: false, reason: "oversized" });

    const almostLimit = `-----BEGIN OPENSSH PRIVATE KEY-----\n${"b".repeat(SSH_PRIVATE_KEY_MAX_BYTES - 80)}\n-----END OPENSSH PRIVATE KEY-----`;
    expect(inspectSshPrivateKeyDraft(almostLimit).ok).toBe(true);
    expect(inspectSshPrivateKeyDraft(composeSshPrivateKeyValue(almostLimit, "x".repeat(80)))).toEqual({ ok: false, reason: "oversized" });
  });

  it("reads a PEM file as the same draft text as paste", async () => {
    const file = new File([opensshPem], "id_ed25519", { type: "text/plain" });
    await expect(inspectSshPrivateKeyFile(file)).resolves.toEqual({ ok: true, text: opensshPem });
  });

  it("rejects a PuTTY file without converting it", async () => {
    const file = new File(["PuTTY-User-Key-File-2: ssh-rsa\nPrivate-Lines: 1\n"], "id_rsa.ppk", { type: "text/plain" });
    await expect(inspectSshPrivateKeyFile(file)).resolves.toEqual({ ok: false, reason: "not_pem" });
  });

  it("rejects empty, oversized, and unreadable files without interpolating contents", async () => {
    await expect(inspectSshPrivateKeyFile(new File([], "empty.pem", { type: "text/plain" }))).resolves.toEqual({ ok: false, reason: "empty" });
    await expect(inspectSshPrivateKeyFile(new File(["x".repeat(SSH_PRIVATE_KEY_MAX_BYTES + 1)], "too-large.pem", { type: "text/plain" }))).resolves.toEqual({ ok: false, reason: "oversized" });

    const unreadable = new File([opensshPem], "unreadable.pem", { type: "text/plain" });
    Object.defineProperty(unreadable, "text", { value: () => Promise.reject(new Error("disk error")) });
    await expect(inspectSshPrivateKeyFile(unreadable)).resolves.toEqual({ ok: false, reason: "unreadable" });
    expect(sshPrivateKeyInspectionMessage("unreadable")).not.toMatch(/BEGIN|disk error|unreadable\.pem/);
    expect(sshPrivateKeyInspectionMessage("oversized")).not.toMatch(/too-large|BEGIN/);
  });
});
