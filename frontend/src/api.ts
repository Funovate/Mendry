import { z } from "zod";

const sourceKindSchema = z.enum(["ssh", "cloud", "mcp"]);
const triggerKindSchema = z.enum(["signed_webhook", "custom_rule"]);
const incidentStatusSchema = z.enum(["Open", "Recovered", "Closed"]);
const unknownRecordSchema = z.record(z.string(), z.unknown());

const currentUserSchema = z.object({
  id: z.string(),
  username: z.string(),
});

const projectSchema = z.object({
  id: z.string(),
  key: z.string(),
  name: z.string(),
  description: z.string(),
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

const dockerContainerSchema = z.object({
	name: z.string(),
	id: z.string(),
	image: z.string(),
	state: z.string(),
	status: z.string(),
});

const sshContainersSchema = z.object({
	containers: z.array(dockerContainerSchema).max(100),
});

const sshLogFileEntrySchema = z.object({
	name: z.string(),
	path: z.string(),
	kind: z.enum(["file", "directory"]),
	readable: z.boolean(),
});

const sshLogFilesSchema = z.object({
	directory: z.string(),
	entries: z.array(sshLogFileEntrySchema).max(100),
	truncated: z.boolean(),
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

const environmentSchema = z.object({
  id: z.string().optional(),
  key: z.string(),
  name: z.string(),
  service: z.string().nullable(),
  version: z.number().optional(),
});

const repositorySchema = z.object({
  id: z.string().optional(),
  remoteUrl: z.string(),
  scmProvider: z.enum(["github", "gitlab", "yunxiao", "gitee", "generic"]),
  transport: z.enum(["https", "ssh"]),
  credentialSecretId: z.string().nullable(),
  productionBranch: z.string(),
  deployedCommit: z.string(),
  version: z.number().optional(),
});

const sourceSchema = z.object({
  id: z.string().optional(),
  kind: sourceKindSchema,
  credentialSecretId: z.string().nullable(),
  config: unknownRecordSchema,
  capabilities: z.array(z.string()),
  enabled: z.boolean(),
  version: z.number().optional(),
});

const triggerSchema = z.object({
  id: z.string().optional(),
  kind: triggerKindSchema,
  signingSecretId: z.string().nullable().optional(),
  inboundUrl: z.string().nullable().optional(),
  config: unknownRecordSchema,
  enabled: z.boolean(),
  version: z.number().optional(),
});

const llmProviderSchema = z.object({
  id: z.string().optional(),
  provider: z.literal("openai"),
  baseUrl: z.string(),
  credentialSecretId: z.string(),
  model: z.string(),
  apiMode: z.enum(["chat_completions", "responses"]),
  version: z.number().optional(),
});

const validationCommandSchema = z.object({
  id: z.string(),
  version: z.number().int().positive(),
  argv: z.array(z.string()).min(1).max(64),
  timeoutSeconds: z.number().int().positive().max(3600),
});

const remediationPolicySchema = z.object({
  agentLoopMode: z.enum(["legacy", "resilient_v1"]),
  executionMode: z.enum(["analysis_only", "auto_hotfix"]),
  validationProfile: z.object({
    enabled: z.boolean().nullable(),
    imageDigest: z.string(),
    workingDirectory: z.string(),
    preparation: z.array(validationCommandSchema).max(16),
    requiredCommands: z.array(validationCommandSchema).max(32),
    cpuLimit: z.number().int().positive(),
    memoryLimitMiB: z.number().int().positive(),
    workspaceLimitMiB: z.number().int().positive(),
  }),
  publication: z.object({
    branchPrefix: z.literal("hotfix/remediation"),
    gitCredentialSecretId: z.string(),
    apiCredentialSecretId: z.string(),
    apiBaseUrl: z.string(),
  }),
  changePolicy: z.object({
    allowedPaths: z.array(z.string()).min(1).max(64),
    deniedPaths: z.array(z.string()).max(64),
    maxChangedFiles: z.number().int().positive().max(30),
    maxChangedLines: z.number().int().positive().max(5000),
  }),
  version: z.number().int().positive().optional(),
});

const hotfixCandidateSchema = z.object({
  directory: z.string(),
  runtime: z.string(),
});

const hotfixStatusSchema = z.enum(["idle", "checking", "needs_selection", "ready", "enabling", "enabled", "blocked"]);

const hotfixCheckSchema = z.object({
  id: z.string(),
  status: hotfixStatusSchema,
  message: z.string(),
  candidates: z.array(hotfixCandidateSchema),
  directory: z.string(),
  runtime: z.string(),
  baselineCommit: z.string(),
  validationSummary: z.string(),
  branchOnly: z.boolean(),
  credentialName: z.string(),
  expiresAt: z.string(),
});

const projectConfigurationSchema = z.object({
  environment: environmentSchema,
  repository: repositorySchema,
  source: sourceSchema,
  trigger: triggerSchema,
  llm: llmProviderSchema.nullish(),
  remediation: remediationPolicySchema,
});

const projectConfigurationDraftSchema = z.object({
  environment: environmentSchema.nullable(),
  repository: repositorySchema.nullable(),
  source: sourceSchema.nullable(),
  trigger: triggerSchema.nullable(),
  llm: llmProviderSchema.nullable(),
  remediation: remediationPolicySchema.nullable(),
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

const customRuleSchema = z.object({
  id: z.string(), name: z.string(), matchType: z.enum(["contains", "regex"]), pattern: z.string(),
  excludePattern: z.string().default(""), threshold: z.number().int(), windowSeconds: z.number().int(), cooldownSeconds: z.number().int(),
});

const logRuleTrialSchema = z.object({
  positiveCount: z.number().int(), negativeCount: z.number().int(),
  rules: z.array(z.object({
    ruleId: z.string(), positiveMatches: z.array(z.number().int()), negativeMatches: z.array(z.number().int()),
    positiveExcluded: z.array(z.number().int()), negativeExcluded: z.array(z.number().int()),
  })),
});

const logProbeStatusSchema = z.object({
  state: z.string(),
  version: z.string(),
  configVersion: z.number().int(),
  checkedAt: z.string(),
  message: z.string(),
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
  lifecycleGeneration: z.number().int().positive(),
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

const remediationAttemptSchema = z.object({
  id: z.string(),
  attemptNumber: z.number().int().positive(),
  status: z.string(),
  origin: z.string(),
  contextVersion: z.number().int().nonnegative(),
  terminalReason: z.string(),
  retryable: z.boolean(),
  version: z.number().int().positive(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

const remediationActionSchema = z.object({
  runId: z.string().min(1),
  seriesId: z.string().min(1),
  status: z.string(),
  generation: z.number().int().positive(),
  attemptNumber: z.number().int().positive(),
  version: z.number().int().positive(),
});

const remediationContinuationInputSchema = z.object({
  generation: z.number().int().positive(),
  runId: z.string().min(1),
  version: z.number().int().positive(),
}).strict();

const remediationRepairInputSchema = z.object({
  generation: z.number().int().positive(),
  expectedRunId: z.string().min(1),
  expectedVersion: z.number().int().positive(),
}).strict();

const remediationBudgetAmountSchema = z.object({
  elapsedSeconds: z.number().int().nonnegative(),
  modelCalls: z.number().int().nonnegative(),
  modelCostCents: z.number().int().nonnegative(),
  toolCalls: z.number().int().nonnegative(),
  evidenceBytes: z.number().int().nonnegative(),
  repositoryBytes: z.number().int().nonnegative(),
}).strict();

const remediationBudgetProjectionSchema = z.object({
  phase: z.string(),
  consumed: remediationBudgetAmountSchema,
  remaining: remediationBudgetAmountSchema,
  unreserved: remediationBudgetAmountSchema,
  recovery: z.object({ min: remediationBudgetAmountSchema }),
}).passthrough();

const remediationCheckpointSchema = z.object({
  sequence: z.number().int().nonnegative(),
  phase: z.string(),
  reason: z.string(),
  observedRunVersion: z.number().int().nonnegative(),
  updatedAt: z.string(),
  validations: z.array(z.object({
    commandId: z.string(), commandVersion: z.number().int().nonnegative(),
    treeHash: z.string(), passed: z.boolean(),
  })).optional().default([]),
  publication: z.object({
    branchRef: z.string(), targetBranch: z.string(), commitHash: z.string(),
    changeRef: z.string().optional(), compareUrl: z.string().optional(),
    targetDiverged: z.boolean().optional(),
  }).optional(),
}).passthrough();

const remediationRecoverySchema = z.object({
  active: z.boolean(),
  kind: z.string().optional(),
  reason: z.string().optional(),
  attempt: z.number().int().nonnegative().optional(),
  attemptedPathClasses: z.array(z.string()),
  nextAction: z.string().optional(),
  remainingBudget: remediationBudgetProjectionSchema.optional(),
});

const remediationReviewSchema = z.object({
  runId: z.string(),
  seriesId: z.string(),
  status: z.string(),
  generation: z.number().int().positive(),
  deployedCommit: z.string(),
  attemptNumber: z.number().int().positive(),
  version: z.number().int().positive(),
  origin: z.string(),
  terminalReason: z.string(),
  manualSuggestion: z.string().optional().default(""),
  retryable: z.boolean(),
  continuationAvailable: z.boolean(),
  attempts: z.array(remediationAttemptSchema).max(64),
  diagnosis: remediationDiagnosisSchema.nullable().optional(),
  plans: z.array(remediationPlanSchema),
  suggestedDiff: z.string(),
  risk: z.string(),
  agentLoopMode: z.string().optional(),
  agentLoopPolicyVersion: z.number().int().nonnegative().optional(),
  checkpoint: remediationCheckpointSchema.optional(),
  recovery: remediationRecoverySchema.optional(),
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

export type SourceKind = z.infer<typeof sourceKindSchema>;
export type TriggerKind = z.infer<typeof triggerKindSchema>;
export type LogRuleTrial = z.infer<typeof logRuleTrialSchema>;
export type LogProbeStatus = z.infer<typeof logProbeStatusSchema>;
export type GeneratedLogRule = z.infer<typeof customRuleSchema>;
export type IncidentStatus = z.infer<typeof incidentStatusSchema>;
export type CurrentUser = z.infer<typeof currentUserSchema>;
export type Project = z.infer<typeof projectSchema>;
export type ProjectSecret = z.infer<typeof projectSecretSchema>;
export type RepositoryRefs = z.infer<typeof repositoryRefsSchema>;
export type DockerContainer = z.infer<typeof dockerContainerSchema>;
export type SshLogFileEntry = z.infer<typeof sshLogFileEntrySchema>;
export type SshLogFiles = z.infer<typeof sshLogFilesSchema>;
export type ProjectConfiguration = z.infer<typeof projectConfigurationSchema>;
export type ProjectConfigurationDraft = z.infer<typeof projectConfigurationDraftSchema>;
export type Observation = z.infer<typeof observationSchema>;
export type ApiIncident = z.infer<typeof incidentSchema>;
export type ListObservationsParams = {
  limit?: number;
  offset?: number;
};
export type ListIncidentsParams = {
  limit?: number;
  offset?: number;
  status?: string;
};
export type RemediationReview = z.infer<typeof remediationReviewSchema>;
export type RemediationCheckpoint = z.infer<typeof remediationCheckpointSchema>;
export type RemediationRecovery = z.infer<typeof remediationRecoverySchema>;
export type RemediationBudgetProjection = z.infer<typeof remediationBudgetProjectionSchema>;
export type RemediationAttempt = z.infer<typeof remediationAttemptSchema>;
export type RemediationAction = z.infer<typeof remediationActionSchema>;
export type RemediationContinuationInput = z.infer<typeof remediationContinuationInputSchema>;
export type RemediationRepairInput = z.infer<typeof remediationRepairInputSchema>;
export type RemediationRetryInput = RemediationContinuationInput;
export type HotfixCandidate = z.infer<typeof hotfixCandidateSchema>;
export type HotfixStatus = z.infer<typeof hotfixStatusSchema>;
export type HotfixCheck = z.infer<typeof hotfixCheckSchema>;
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

const startRemediation = (projectKey: string, incidentId: string, generation: number) =>
  requestData(projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/remediation/start`), remediationActionSchema, {
    method: "POST",
    body: JSON.stringify({ generation }),
  });

const retryRemediation = (projectKey: string, incidentId: string, input: RemediationContinuationInput) => {
  const path = projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/remediation/retry`);
  const parsed = remediationContinuationInputSchema.safeParse(input);
  if (!parsed.success) {
    return Promise.reject(new ApiContractError(path, "The remediation request does not match the frontend contract.", parsed.error));
  }
  return requestData(path, remediationActionSchema, {
    method: "POST",
    body: JSON.stringify(parsed.data),
  });
};

const repairRemediation = (projectKey: string, incidentId: string, input: RemediationRepairInput) => {
  const path = projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/remediation/repair`);
  const parsed = remediationRepairInputSchema.safeParse(input);
  if (!parsed.success) {
    return Promise.reject(new ApiContractError(path, "The remediation request does not match the frontend contract.", parsed.error));
  }
  return requestData(path, remediationActionSchema, {
    method: "POST",
    body: JSON.stringify(parsed.data),
  });
};

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
  probeSSHContainers: (projectKey: string, input: { host: string; port: number; user: string; credentialSecretId: string }) =>
    requestData(projectPath(projectKey, "/configuration/source/ssh/containers"), sshContainersSchema, { method: "POST", body: JSON.stringify(input) }),
  browseSSHLogFiles: (projectKey: string, input: { host: string; port: number; user: string; credentialSecretId: string; path: string }) =>
    requestData(projectPath(projectKey, "/configuration/source/ssh/log-files"), sshLogFilesSchema, { method: "POST", body: JSON.stringify(input) }),
  probeLLMModels: (projectKey: string, input: { baseUrl: string; credentialSecretId: string }) =>
    requestData(projectPath(projectKey, "/llm/models"), llmModelsSchema, { method: "POST", body: JSON.stringify(input) }),
  probeLLMChat: (projectKey: string, input: { baseUrl: string; credentialSecretId: string; model: string; apiMode: "chat_completions" | "responses" }) =>
    requestData(projectPath(projectKey, "/llm/chat"), llmChatProbeSchema, { method: "POST", body: JSON.stringify(input) }),
  getConfiguration: (projectKey: string, signal?: AbortSignal) => requestData(projectPath(projectKey, "/configuration"), projectConfigurationSchema, { signal }),
  getConfigurationDraft: (projectKey: string, signal?: AbortSignal) => requestData(projectPath(projectKey, "/configuration/draft"), projectConfigurationDraftSchema, { signal }),
  putConfiguration: (projectKey: string, configuration: ProjectConfiguration) =>
    requestData(projectPath(projectKey, "/configuration"), projectConfigurationSchema, { method: "PUT", body: JSON.stringify(configuration) }),
  putConfigurationEnvironment: (projectKey: string, input: Omit<ProjectConfiguration["environment"], "id" | "version">) =>
    requestData(projectPath(projectKey, "/configuration/environment"), environmentSchema, { method: "PUT", body: JSON.stringify(input) }),
  putConfigurationRepository: (projectKey: string, input: Omit<ProjectConfiguration["repository"], "id" | "version">) =>
    requestData(projectPath(projectKey, "/configuration/repository"), repositorySchema, { method: "PUT", body: JSON.stringify(input) }),
  putConfigurationSource: (projectKey: string, input: Omit<ProjectConfiguration["source"], "id" | "version">) =>
    requestData(projectPath(projectKey, "/configuration/source"), sourceSchema, { method: "PUT", body: JSON.stringify(input) }),
  putConfigurationTrigger: (projectKey: string, input: Omit<ProjectConfiguration["trigger"], "id" | "version" | "inboundUrl">) =>
    requestData(projectPath(projectKey, "/configuration/trigger"), triggerSchema, { method: "PUT", body: JSON.stringify(input) }),
  putConfigurationLLM: (projectKey: string, input: Omit<NonNullable<ProjectConfiguration["llm"]>, "id" | "version">) =>
    requestData(projectPath(projectKey, "/configuration/llm"), llmProviderSchema, { method: "PUT", body: JSON.stringify(input) }),
  putConfigurationRemediation: (projectKey: string, input: Omit<ProjectConfiguration["remediation"], "version">) => {
    const cleanPayload = { ...(input as ProjectConfiguration["remediation"]) };
    delete (cleanPayload as { version?: unknown }).version;
    return requestData(projectPath(projectKey, "/configuration/remediation-policy"), remediationPolicySchema, { method: "PUT", body: JSON.stringify(cleanPayload) });
  },
  checkAutoHotfix: (projectKey: string, input?: { directory?: string }) =>
    requestData(projectPath(projectKey, "/configuration/auto-hotfix/check"), hotfixCheckSchema, { method: "POST", body: JSON.stringify({ directory: input?.directory ?? "" }) }),
  getAutoHotfixCheck: (projectKey: string, signal?: AbortSignal) =>
    requestData(projectPath(projectKey, "/configuration/auto-hotfix/check"), hotfixCheckSchema, { signal }),
  enableAutoHotfix: (projectKey: string, checkId: string) =>
    requestData(projectPath(projectKey, "/configuration/auto-hotfix/enable"), remediationPolicySchema, { method: "POST", body: JSON.stringify({ checkId }) }),
  rotateWebhookToken: (projectKey: string) =>
    requestData(projectPath(projectKey, "/configuration/webhook-token"), webhookTokenSchema, { method: "POST", body: "{}" }),
  trialLogRules: (projectKey: string, input: { config: unknown; positive: string[]; negative: string[] }) =>
    requestData(projectPath(projectKey, "/configuration/log-rule/test"), logRuleTrialSchema, { method: "POST", body: JSON.stringify(input) }),
  generateLogRule: (projectKey: string, input: { intent: string; sample: string }) =>
    requestData(projectPath(projectKey, "/configuration/log-rule/generate"), customRuleSchema, { method: "POST", body: JSON.stringify(input) }),
  getLogProbeStatus: (projectKey: string, signal?: AbortSignal) =>
    requestData(projectPath(projectKey, "/configuration/log-probe"), logProbeStatusSchema, { signal }),
  installLogProbe: (projectKey: string) =>
    requestData(projectPath(projectKey, "/configuration/log-probe"), logProbeStatusSchema, { method: "POST", body: "{}" }),
  uninstallLogProbe: (projectKey: string) =>
    requestData(projectPath(projectKey, "/configuration/log-probe"), logProbeStatusSchema, { method: "DELETE" }),
  listObservations: (projectKey: string, params?: ListObservationsParams, signal?: AbortSignal) => {
    const searchParams = new URLSearchParams();
    if (params?.limit !== undefined) searchParams.set("limit", String(params.limit));
    if (params?.offset !== undefined) searchParams.set("offset", String(params.offset));
    const qs = searchParams.toString();
    return requestList(projectPath(projectKey, `/observations${qs ? `?${qs}` : ""}`), observationSchema, { signal });
  },
  listIncidents: (
    projectKey: string,
    paramsOrSignal?: ListIncidentsParams | AbortSignal,
    signal?: AbortSignal
  ) => {
    let params: ListIncidentsParams | undefined;
    let actualSignal = signal;
    if (paramsOrSignal instanceof AbortSignal) {
      actualSignal = paramsOrSignal;
    } else if (paramsOrSignal) {
      params = paramsOrSignal;
    }
    const searchParams = new URLSearchParams();
    if (params?.limit !== undefined) searchParams.set("limit", String(params.limit));
    if (params?.offset !== undefined) searchParams.set("offset", String(params.offset));
    if (params?.status && params.status !== "All") searchParams.set("status", params.status);
    const qs = searchParams.toString();
    return requestList(projectPath(projectKey, `/incidents${qs ? `?${qs}` : ""}`), incidentSchema, { signal: actualSignal });
  },
  getIncident: (projectKey: string, incidentId: string, signal?: AbortSignal) =>
    requestData(projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}`), incidentSchema, { signal }),
  updateIncidentStatus: (projectKey: string, incidentId: string, status: IncidentStatus) =>
    requestData(projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/status`), incidentSchema, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),
  getRemediation: (projectKey: string, incidentId: string, signal?: AbortSignal) =>
    requestData(projectPath(projectKey, `/incidents/${encodeURIComponent(incidentId)}/remediation`), remediationReviewSchema, { signal }),
  startRemediation,
  retryRemediation,
  repairRemediation,
  continueRemediation: retryRemediation,
};

const notificationChannelSchema = z.object({
  id: z.string(), name: z.string(), platform: z.enum(["telegram", "feishu", "wecom"]),
  enabled: z.boolean(), hasCredentials: z.boolean(), createdAt: z.string(), updatedAt: z.string(),
});
const notificationDeliverySchema = z.object({
  id: z.string(), channelId: z.string(), channelName: z.string(), platform: z.string(),
  kind: z.enum(["trigger", "result"]), incidentNumber: z.number(), generation: z.number(),
  state: z.enum(["pending", "sending", "delivered", "failed", "cancelled"]), attempts: z.number(),
  lastError: z.string(), createdAt: z.string(), nextAttemptAt: z.string(), deliveredAt: z.string().nullable(),
});
export type NotificationChannel = z.infer<typeof notificationChannelSchema>;
export type NotificationDelivery = z.infer<typeof notificationDeliverySchema>;
export type NotificationChannelInput = {
  name: string; platform: NotificationChannel["platform"]; enabled: boolean;
  credentials?: { botToken?: string; chatId?: string; webhookUrl?: string; signingSecret?: string };
};
export const notificationApi = {
  listChannels: (key: string, signal?: AbortSignal) => requestList(projectPath(key, "/notifications/channels"), notificationChannelSchema, { signal }),
  saveChannel: (key: string, input: NotificationChannelInput, id?: string) => requestData(projectPath(key, `/notifications/channels${id ? `/${encodeURIComponent(id)}` : ""}`), notificationChannelSchema, { method: id ? "PUT" : "POST", body: JSON.stringify(input) }),
  deleteChannel: (key: string, id: string) => requestEmpty(projectPath(key, `/notifications/channels/${encodeURIComponent(id)}`), { method: "DELETE" }),
  testChannel: (key: string, id: string) => requestData(projectPath(key, `/notifications/channels/${encodeURIComponent(id)}/test`), z.object({ sent: z.boolean() }), { method: "POST" }),
  listDeliveries: (key: string, signal?: AbortSignal) => requestList(projectPath(key, "/notifications/deliveries"), notificationDeliverySchema, { signal }),
  retryDelivery: (key: string, id: string) => requestData(projectPath(key, `/notifications/deliveries/${encodeURIComponent(id)}/retry`), z.object({ queued: z.boolean() }), { method: "POST" }),
};

export function messageFromError(error: unknown): string {
  if (error instanceof ApiError || error instanceof ApiContractError) return error.message;
  if (error instanceof Error) return error.message;
  return "The request could not be completed.";
}
