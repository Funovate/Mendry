import { z } from "zod";

const systemRoleSchema = z.enum(["admin", "operator", "viewer"]);
const projectRoleSchema = z.enum(["admin", "operator", "viewer"]);
const sourceKindSchema = z.enum(["ssh", "cloud", "mcp"]);
const triggerKindSchema = z.enum(["signed_webhook", "custom_rule"]);
const incidentStatusSchema = z.enum(["Open", "Recovered", "Closed"]);
const unknownRecordSchema = z.record(z.string(), z.unknown());

const currentUserSchema = z.object({
  id: z.string(),
  username: z.string(),
  role: systemRoleSchema,
});

const projectCapabilitiesSchema = z.object({
  read: z.boolean(),
  writeIncidents: z.boolean(),
  manageMembers: z.boolean(),
  manageConfiguration: z.boolean(),
});

const projectSchema = z.object({
  id: z.string(),
  key: z.string(),
  name: z.string(),
  description: z.string(),
  role: projectRoleSchema,
  capabilities: projectCapabilitiesSchema,
  version: z.number(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

const projectMemberSchema = z.object({
  userId: z.string(),
  username: z.string(),
  role: projectRoleSchema,
  version: z.number(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

const gitBranchSchema = z.object({
  name: z.string(),
  commit: z.string(),
});

const repositoryRefsSchema = z.object({
  defaultBranch: z.string(),
  deployedCommit: z.string(),
  branches: z.array(gitBranchSchema),
});

const projectSecretSchema = z.object({
  id: z.string(),
  name: z.string(),
  kind: z.enum(["ssh_password", "ssh_private_key", "http_bearer", "http_header", "webhook_hmac", "git_credential"]),
  keyVersion: z.number(),
  version: z.number(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

const projectConfigurationSchema = z.object({
  environment: z.object({
    id: z.string().optional(),
    key: z.string(),
    name: z.string(),
    service: z.string().nullable(),
    version: z.number().optional(),
  }),
  repository: z.object({
    id: z.string().optional(),
    remoteUrl: z.string(),
    scmProvider: z.enum(["github", "gitlab", "yunxiao", "gitee", "generic"]),
    transport: z.enum(["https", "ssh"]),
    credentialSecretId: z.string().nullable(),
    productionBranch: z.string(),
    deployedCommit: z.string(),
    version: z.number().optional(),
  }),
  source: z.object({
    id: z.string().optional(),
    name: z.string(),
    kind: sourceKindSchema,
    credentialSecretId: z.string().nullable(),
    config: unknownRecordSchema,
    capabilities: z.array(z.string()),
    enabled: z.boolean(),
    version: z.number().optional(),
  }),
  trigger: z.object({
    id: z.string().optional(),
    name: z.string(),
    kind: triggerKindSchema,
    signingSecretId: z.string().nullable().optional(),
    inboundUrl: z.string().nullable().optional(),
    config: unknownRecordSchema,
    enabled: z.boolean(),
    version: z.number().optional(),
  }),
  llm: z.object({
    id: z.string().optional(),
    provider: z.literal("openai"),
    baseUrl: z.string(),
    credentialSecretId: z.string(),
    model: z.string(),
    version: z.number().optional(),
  }).nullish(),
});

const llmModelsSchema = z.object({
  models: z.array(z.string()),
});

const llmChatProbeSchema = z.object({
  status: z.literal("ok"),
});

const webhookTokenSchema = z.object({
  inboundUrl: z.string(),
});

const observationSchema = z.object({
  id: z.string(),
  environmentId: z.string(),
  sourceId: z.string(),
  service: z.string().nullable(),
  occurredAt: z.string(),
  level: z.string(),
  message: z.string(),
  host: z.string().nullable(),
  requestId: z.string().nullable(),
  fingerprint: z.string(),
  attributes: unknownRecordSchema,
  ingestedAt: z.string(),
});

const incidentSchema = z.object({
  id: z.string(),
  title: z.string(),
  fingerprint: z.string(),
  status: incidentStatusSchema,
  priority: z.string(),
  source: z.string(),
  sourceId: z.string(),
  environmentId: z.string(),
  firstSeen: z.string(),
  lastSeen: z.string(),
  occurrenceCount: z.number(),
  hostCount: z.number(),
  muted: z.boolean(),
  notificationSummary: z.string(),
  version: z.number(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

const auditEventSchema = z.object({
  id: z.string(),
  actorUserId: z.string().nullable(),
  action: z.string(),
  targetType: z.string(),
  targetId: z.string().nullable(),
  summary: z.string(),
  metadata: unknownRecordSchema,
  occurredAt: z.string(),
});

const remediationDiagnosisSchema = z.object({
  fixability: z.string(),
  confidence: z.number(),
  causalReasoning: z.string(),
  evidenceRefs: z.array(z.string()),
  contradictions: z.array(z.string()),
  missingEvidence: z.array(z.string()),
  recommendedNextAction: z.string(),
});

const remediationPlanSchema = z.object({
  planId: z.string(),
  intendedBehavior: z.string(),
  risk: z.string(),
  rationale: z.string(),
  evidenceRefs: z.array(z.string()),
  affectedFiles: z.array(z.string()),
  rollbackStrategy: z.string().optional(),
  recommended: z.boolean(),
});

const remediationReviewSchema = z.object({
  runId: z.string(),
  seriesId: z.string(),
  status: z.string(),
  generation: z.number(),
  deployedCommit: z.string(),
  diagnosis: remediationDiagnosisSchema.nullable().optional(),
  plans: z.array(remediationPlanSchema),
  suggestedDiff: z.string(),
  risk: z.string(),
});

const errorEnvelopeSchema = z.object({
  error: z.object({
    code: z.string().optional(),
    message: z.string().optional(),
    requestId: z.string().optional(),
  }).optional(),
});

const successMetaSchema = z.object({
  requestId: z.string(),
  durationMs: z.number().int().nonnegative(),
  total: z.number().int().nonnegative().optional(),
});

const listSuccessMetaSchema = successMetaSchema.extend({
  total: z.number().int().nonnegative(),
});

const successEnvelope = <T extends z.ZodType>(schema: T) => z.object({
  code: z.literal("ok"),
  message: z.literal("OK"),
  data: schema,
  meta: successMetaSchema,
});

const listSuccessEnvelope = <T extends z.ZodType>(schema: T) => z.object({
  code: z.literal("ok"),
  message: z.literal("OK"),
  data: z.array(schema),
  meta: listSuccessMetaSchema,
});

export type SystemRole = z.infer<typeof systemRoleSchema>;
export type ProjectRole = z.infer<typeof projectRoleSchema>;
export type SourceKind = z.infer<typeof sourceKindSchema>;
export type TriggerKind = z.infer<typeof triggerKindSchema>;
export type IncidentStatus = z.infer<typeof incidentStatusSchema>;
export type CurrentUser = z.infer<typeof currentUserSchema>;
export type ProjectCapabilities = z.infer<typeof projectCapabilitiesSchema>;
export type Project = z.infer<typeof projectSchema>;
export type ProjectMember = z.infer<typeof projectMemberSchema>;
export type ProjectSecret = z.infer<typeof projectSecretSchema>;
export type RepositoryRefs = z.infer<typeof repositoryRefsSchema>;
export type ProjectConfiguration = z.infer<typeof projectConfigurationSchema>;
export type Observation = z.infer<typeof observationSchema>;
export type ApiIncident = z.infer<typeof incidentSchema>;
export type AuditEvent = z.infer<typeof auditEventSchema>;
export type RemediationReview = z.infer<typeof remediationReviewSchema>;
export type ListResult<T> = { items: T[]; total: number };

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly requestId?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class ApiContractError extends Error {
  constructor(public readonly path: string, message: string, public readonly cause?: unknown) {
    super(message);
    this.name = "ApiContractError";
  }
}

async function errorFromResponse(response: Response): Promise<ApiError> {
  let envelope: z.infer<typeof errorEnvelopeSchema> = {};
  try {
    const parsed = errorEnvelopeSchema.safeParse(await response.json());
    if (parsed.success) envelope = parsed.data;
  } catch {
    // HTTP status remains authoritative when a gateway returns non-JSON.
  }
  return new ApiError(
    response.status,
    envelope.error?.code ?? "request_failed",
    envelope.error?.message ?? `Request failed with status ${response.status}.`,
    envelope.error?.requestId,
  );
}

async function request<T>(path: string, schema: z.ZodType<T>, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "include",
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });

  if (!response.ok) throw await errorFromResponse(response);

  let body: unknown;
  try {
    body = await response.json();
  } catch (error) {
    throw new ApiContractError(path, "The server returned invalid JSON.", error);
  }
  const parsed = schema.safeParse(body);
  if (!parsed.success) {
    throw new ApiContractError(path, "The server response does not match the frontend contract.", parsed.error);
  }
  return parsed.data;
}

async function requestData<T>(path: string, schema: z.ZodType<T>, init?: RequestInit): Promise<T> {
  return request(path, successEnvelope(schema), init).then((envelope) => envelope.data);
}

async function requestList<T>(path: string, schema: z.ZodType<T>, init?: RequestInit): Promise<ListResult<T>> {
  const envelope = await request(path, listSuccessEnvelope(schema), init);
  return { items: envelope.data, total: envelope.meta.total };
}

async function requestEmpty(path: string, init?: RequestInit): Promise<void> {
  const response = await fetch(path, {
    ...init,
    credentials: "include",
    headers: {
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });
  if (!response.ok) throw await errorFromResponse(response);
  if (response.status !== 204) {
    throw new ApiContractError(path, `Expected an empty response, received status ${response.status}.`);
  }
}

const projectPath = (projectKey: string, suffix = "") =>
  `/api/v1/projects/${encodeURIComponent(projectKey)}${suffix}`;

export const api = {
  me: (signal?: AbortSignal) => requestData("/api/v1/auth/me", currentUserSchema, { signal }),
  login: (username: string, password: string) => requestData("/api/v1/auth/login", currentUserSchema, {
    method: "POST",
    body: JSON.stringify({ username, password }),
  }),
  logout: () => requestEmpty("/api/v1/auth/logout", { method: "POST", body: "{}" }),
  listProjects: (signal?: AbortSignal) => requestList("/api/v1/projects?limit=100", projectSchema, { signal }),
  createProject: (input: { key: string; name: string; description: string }) => requestData("/api/v1/projects", projectSchema, {
    method: "POST",
    body: JSON.stringify(input),
  }),
  listMembers: (projectKey: string, signal?: AbortSignal) => requestList(projectPath(projectKey, "/members"), projectMemberSchema, { signal }),
  upsertMember: (projectKey: string, username: string, role: ProjectRole) =>
    requestData(projectPath(projectKey, `/members/${encodeURIComponent(username)}`), projectMemberSchema, {
      method: "PUT",
      body: JSON.stringify({ role }),
    }),
  deleteMember: (projectKey: string, username: string) =>
    requestEmpty(projectPath(projectKey, `/members/${encodeURIComponent(username)}`), { method: "DELETE" }),
  listSecrets: (projectKey: string, signal?: AbortSignal) => requestList(projectPath(projectKey, "/secrets"), projectSecretSchema, { signal }),
  updateProjectName: (projectKey: string, name: string) =>
    requestData(projectPath(projectKey), projectSchema, { method: "PATCH", body: JSON.stringify({ name }) }),
  createSecret: (projectKey: string, input: { name: string; kind: ProjectSecret["kind"]; value: string }) =>
    requestData(projectPath(projectKey, "/secrets"), projectSecretSchema, { method: "POST", body: JSON.stringify(input) }),
  updateSecret: (projectKey: string, secretId: string, input: { name: string; value?: string }) =>
    requestData(projectPath(projectKey, `/secrets/${encodeURIComponent(secretId)}`), projectSecretSchema, {
      method: "PATCH",
      body: JSON.stringify(input.value === undefined ? { name: input.name } : { name: input.name, value: input.value }),
    }),
  probeRepositoryRefs: (projectKey: string, input: { remoteUrl: string; transport: "https" | "ssh"; credentialSecretId: string }) =>
    requestData(projectPath(projectKey, "/repository/refs"), repositoryRefsSchema, { method: "POST", body: JSON.stringify(input) }),
  probeLLMModels: (projectKey: string, input: { baseUrl: string; credentialSecretId: string }) =>
    requestData(projectPath(projectKey, "/llm/models"), llmModelsSchema, { method: "POST", body: JSON.stringify(input) }),
  probeLLMChat: (projectKey: string, input: { baseUrl: string; credentialSecretId: string; model: string }) =>
    requestData(projectPath(projectKey, "/llm/chat"), llmChatProbeSchema, { method: "POST", body: JSON.stringify(input) }),
  getConfiguration: (projectKey: string, signal?: AbortSignal) => requestData(projectPath(projectKey, "/configuration"), projectConfigurationSchema, { signal }),
  putConfiguration: (projectKey: string, configuration: ProjectConfiguration) =>
    requestData(projectPath(projectKey, "/configuration"), projectConfigurationSchema, { method: "PUT", body: JSON.stringify(configuration) }),
  rotateWebhookToken: (projectKey: string) =>
    requestData(projectPath(projectKey, "/configuration/webhook-token"), webhookTokenSchema, { method: "POST", body: "{}" }),
  listObservations: (projectKey: string, signal?: AbortSignal) => requestList(projectPath(projectKey, "/observations?limit=100"), observationSchema, { signal }),
  listIncidents: (projectKey: string, signal?: AbortSignal) => requestList(projectPath(projectKey, "/incidents?limit=100"), incidentSchema, { signal }),
  updateIncidentStatus: (projectKey: string, incidentId: string, status: IncidentStatus) =>
    requestData(projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/status`), incidentSchema, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),
  getRemediation: (projectKey: string, incidentId: string, signal?: AbortSignal) =>
    requestData(projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/remediation`), remediationReviewSchema, { signal }),
  listAuditEvents: (projectKey: string, signal?: AbortSignal) => requestList(projectPath(projectKey, "/audit-events?limit=100"), auditEventSchema, { signal }),
};

export function messageFromError(error: unknown): string {
  if (error instanceof ApiError || error instanceof ApiContractError) return error.message;
  if (error instanceof Error) return error.message;
  return "The request could not be completed.";
}
