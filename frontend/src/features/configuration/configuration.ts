import { ApiError, type ProjectConfiguration, type ProjectSecret, type SourceKind, type TriggerKind } from "../../api";

export function readConfigString(config: Record<string, unknown> | undefined, key: string, fallback: string): string {
  const value = config?.[key];
  return typeof value === "string" ? value : fallback;
}

export function readConfigNumber(config: Record<string, unknown> | undefined, key: string, fallback: number): number {
  const value = config?.[key];
  return typeof value === "number" ? value : fallback;
}

export function readConfigStrings(config: Record<string, unknown> | undefined, key: string, fallback: string[]): string[] {
  const value = config?.[key];
  return Array.isArray(value) && value.every((item) => typeof item === "string") ? value : fallback;
}

export function readStringRecord(config: Record<string, unknown> | undefined, key: string): Record<string, string> {
  const value = config?.[key];
  if (typeof value !== "object" || value === null || Array.isArray(value)) return {};
  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
}

export function defaultSourceCapabilities(kind: SourceKind): string[] {
  if (kind === "cloud") return ["pull_collection", "context_collection", "metric_collection"];
  return ["pull_collection", "context_collection"];
}

export type SourceConfigInput = {
  endpoint: string;
  mcpTransport: string;
  mcpHeaders: string;
  evidenceProfile: string;
  queryScope: string;
  host: string;
  port: number;
  user: string;
  projectFolder: string;
  logPath: string;
  readMode: string;
  cloudProvider: string;
  cloudRegion: string;
  resource: string;
};

export function buildSourceConfig(kind: SourceKind, input: SourceConfigInput): Record<string, unknown> {
  if (kind === "mcp") {
    const headers: unknown = JSON.parse(input.mcpHeaders || "{}");
    if (typeof headers !== "object" || headers === null || Array.isArray(headers) || Object.values(headers).some((value) => typeof value !== "string")) {
      throw new Error("MCP headers must be a JSON object with string values.");
    }
    return {
      schemaVersion: 1,
      endpoint: input.endpoint.trim(),
      transport: input.mcpTransport,
      headers,
      evidenceProfile: input.evidenceProfile.trim(),
      queryScope: input.queryScope.trim(),
    };
  }
  if (kind === "ssh") {
    return {
      schemaVersion: 1,
      host: input.host.trim(),
      port: input.port,
      user: input.user.trim(),
      projectFolder: input.projectFolder.trim(),
      logPath: input.logPath.trim(),
      mode: input.readMode,
    };
  }
  return {
    schemaVersion: 1,
    provider: input.cloudProvider.trim(),
    region: input.cloudRegion.trim(),
    resource: input.resource.trim(),
  };
}

export function buildTriggerConfig(kind: TriggerKind, input: { eventTypes: string; deduplicationKey: string; groupingWindowSeconds: number; matchExpression: string }): Record<string, unknown> {
  if (kind === "signed_webhook") {
    const eventTypes = input.eventTypes.split(",").map((value) => value.trim()).filter(Boolean);
    return {
      schemaVersion: 1,
      eventTypes: eventTypes.length > 0 ? eventTypes : ["alarm"],
      deduplicationKey: input.deduplicationKey.trim() || "title",
    };
  }
  return {
    schemaVersion: 1,
    groupingWindowSeconds: input.groupingWindowSeconds,
    matchExpression: input.matchExpression.trim(),
  };
}

export function configurationOrNull(error: unknown): ProjectConfiguration | null {
  if (error instanceof ApiError && error.status === 404 && error.code === "configuration_not_found") return null;
  throw error;
}

export type GitTransport = "https" | "ssh";

export function gitSecretKindsForTransport(transport: GitTransport): ProjectSecret["kind"][] {
  return transport === "https" ? ["git_credential"] : ["ssh_private_key", "ssh_password"];
}

export function filterGitSecrets(secrets: ProjectSecret[], transport: GitTransport): ProjectSecret[] {
  const kinds = new Set(gitSecretKindsForTransport(transport));
  return secrets.filter((secret) => kinds.has(secret.kind));
}

export function composeHttpsGitCredentialValue(username: string, secret: string): string {
  const trimmedUsername = username.trim();
  const trimmedSecret = secret.trim();
  return trimmedUsername ? `${trimmedUsername}:${trimmedSecret}` : trimmedSecret;
}

export function composeSshPrivateKeyValue(privateKey: string, passphrase: string): string {
  const key = privateKey.replace(/\s+$/u, "");
  const trimmedPassphrase = passphrase.trim();
  return trimmedPassphrase ? `${key}\n${trimmedPassphrase}` : key;
}

export const SSH_PRIVATE_KEY_MAX_BYTES = 65519;

export type SshPrivateKeyInspectionReason = "empty" | "unreadable" | "oversized" | "not_pem";

export type SshPrivateKeyInspection =
  | { ok: true; text: string }
  | { ok: false; reason: SshPrivateKeyInspectionReason };

function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

function looksLikePemPrivateKey(text: string): boolean {
  const begin = /-----BEGIN [^\n]*PRIVATE KEY-----/.exec(text);
  if (!begin || begin.index === undefined) return false;
  return /-----END [^\n]*PRIVATE KEY-----/.test(text.slice(begin.index + begin[0].length));
}

export function inspectSshPrivateKeyDraft(raw: string): SshPrivateKeyInspection {
  if (raw.trim() === "") return { ok: false, reason: "empty" };
  if (!looksLikePemPrivateKey(raw)) return { ok: false, reason: "not_pem" };
  if (utf8ByteLength(raw) > SSH_PRIVATE_KEY_MAX_BYTES) return { ok: false, reason: "oversized" };
  return { ok: true, text: raw };
}

export function sshPrivateKeyInspectionMessage(reason: SshPrivateKeyInspectionReason): string {
  if (reason === "empty") return "The SSH private key is empty.";
  if (reason === "unreadable") return "The SSH private key file could not be read.";
  if (reason === "oversized") return "The SSH private key exceeds the 65519-byte limit.";
  return "The selected content is not a PEM or OpenSSH private key.";
}

function readFileText(file: File): Promise<string> {
  if (typeof file.text === "function") return file.text();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.onerror = () => reject(reader.error ?? new Error("unreadable"));
    reader.readAsText(file);
  });
}

export async function inspectSshPrivateKeyFile(file: File): Promise<SshPrivateKeyInspection> {
  if (file.size === 0) return { ok: false, reason: "empty" };
  if (file.size > SSH_PRIVATE_KEY_MAX_BYTES) return { ok: false, reason: "oversized" };
  try {
    return inspectSshPrivateKeyDraft(await readFileText(file));
  } catch {
    return { ok: false, reason: "unreadable" };
  }
}

export type GitSecretReplacement = {
  complete: boolean;
  value?: string;
};

export function composeGitSecretReplacement(
  kind: ProjectSecret["kind"],
  draft: { username: string; secret: string; privateKey: string; passphrase: string; sshPassword: string },
): GitSecretReplacement {
  if (kind === "git_credential") {
    const started = draft.username.trim() !== "" || draft.secret.trim() !== "";
    if (!started) return { complete: true };
    if (draft.secret.trim() === "") return { complete: false };
    return { complete: true, value: composeHttpsGitCredentialValue(draft.username, draft.secret) };
  }
  if (kind === "ssh_password") {
    const started = draft.username.trim() !== "" || draft.sshPassword.trim() !== "";
    if (!started) return { complete: true };
    if (draft.sshPassword.trim() === "") return { complete: false };
    return { complete: true, value: composeHttpsGitCredentialValue(draft.username, draft.sshPassword) };
  }
  const started = draft.privateKey.trim() !== "" || draft.passphrase.trim() !== "";
  if (!started) return { complete: true };
  if (draft.privateKey.trim() === "") return { complete: false };
  return { complete: true, value: composeSshPrivateKeyValue(draft.privateKey, draft.passphrase) };
}

export function compatibleGitSecretId(secretId: string, secrets: ProjectSecret[], transport: GitTransport): string {
  return filterGitSecrets(secrets, transport).some((secret) => secret.id === secretId) ? secretId : "";
}
