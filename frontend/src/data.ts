export type Role = "admin" | "operator" | "viewer";
export type IncidentStatus = "Open" | "Recovered" | "Closed";

export interface Incident {
  id: string;
  title: string;
  fingerprint: string;
  status: IncidentStatus;
  priority: "Info" | "P2" | "P1";
  source: string;
  lastSeen: string;
  count: number;
  hosts: number;
  muted?: boolean;
  notification: string;
}

export const incidents: Incident[] = [
  {
    id: "INC-2048",
    title: "Validator locale fr is not registered",
    fingerprint: "b2a8:validator-locale",
    status: "Open",
    priority: "Info",
    source: "cls-backend",
    lastSeen: "2 min ago",
    count: 47,
    hosts: 2,
    notification: "Lifecycle default",
  },
  {
    id: "INC-2039",
    title: "PostgreSQL pool acquisition timeout",
    fingerprint: "a710:pg-pool-timeout",
    status: "Recovered",
    priority: "P2",
    source: "postgres-monitor",
    lastSeen: "42 min ago",
    count: 186,
    hosts: 4,
    notification: "Recovery sent",
  },
  {
    id: "INC-2024",
    title: "RabbitMQ consumer retry exhausted",
    fingerprint: "6c43:consumer-retry",
    status: "Open",
    priority: "Info",
    source: "cls-backend",
    lastSeen: "1 h ago",
    count: 8,
    hosts: 1,
    muted: true,
    notification: "Muted",
  },
  {
    id: "INC-1988",
    title: "Nacos configuration checksum mismatch",
    fingerprint: "e081:nacos-checksum",
    status: "Closed",
    priority: "Info",
    source: "cls-backend",
    lastSeen: "Aug 08, 16:03",
    count: 19,
    hosts: 1,
    notification: "Closed by operator",
  },
];

export const validatorIncident = incidents[0];

export const evidence = [
  {
    id: "EV-104",
    type: "Error occurrence",
    source: "Tencent CLS / cls-backend",
    detail: "server.log near 2026-08-08 15:52:14.750",
    scope: "project: real-estate, environment: production",
    collected: "15:53:02 UTC",
    retained: "Expires Sep 07, 2026",
  },
  {
    id: "EV-105",
    type: "Request correlation",
    source: "Tencent CLS / cls-backend",
    detail: "No request ID present in terminal middleware log",
    scope: "same host and 5 minute window",
    collected: "15:53:04 UTC",
    retained: "Expires Sep 07, 2026",
  },
  {
    id: "EV-106",
    type: "Trend",
    source: "Error group aggregation",
    detail: "47 occurrences across 2 hosts in 18 minutes",
    scope: "fingerprint b2a8:validator-locale",
    collected: "15:53:08 UTC",
    retained: "Incident metadata: 180 days",
  },
];

export const evidenceDetails = {
  "EV-104": {
    id: "EV-104",
    title: "Validator error on backend host",
    source: "Tencent CLS / cls-backend",
    query: 'topic:server-log AND level:ERROR AND message:"validator instance"',
    timeWindow: "2026-08-08 15:48:00 to 15:56:00 UTC",
    hash: "sha256:88d9a2e5...7d91",
    collector: "cls-mcp@1.4.2, read-only context scope",
    citedBy: "Observed fact: locale resolution reaches an unregistered validator instance.",
    log: `[2026-08-08T15:52:14.750Z] ERROR api/http_encoder.go:79\nservice=real-estate-api env=production host=VM-6-17-tencentos\nrequest.locale=fr request_id=<missing>\nvalidator instance for language 'fr' (normalized to 'fr') not registered\nstack=api.NewHTTPError > encoder.WriteValidationError > validator.ForLocale`,
  },
  "EV-105": {
    id: "EV-105",
    title: "Missing request correlation",
    source: "Tencent CLS / cls-backend",
    query: "host:(VM-6-17-tencentos OR VM-6-21-tencentos) AND time:[15:48,15:56]",
    timeWindow: "2026-08-08 15:48:00 to 15:56:00 UTC",
    hash: "sha256:8a347f10...2b1e",
    collector: "cls-mcp@1.4.2, read-only context scope",
    citedBy: "Limitation: request IDs are absent from terminal error middleware logs.",
    log: `[2026-08-08T15:52:14.750Z] ERROR api/http_encoder.go:79\nrequest_id=<missing> trace_id=<redacted> locale=fr\nvalidator instance for language 'fr' (normalized to 'fr') not registered\n\n[2026-08-08T15:51:49.403Z] ERROR api/http_encoder.go:79\nrequest_id=<missing> trace_id=<redacted> locale=fr`,
  },
  "EV-106": {
    id: "EV-106",
    title: "Occurrence aggregation",
    source: "Error group aggregation",
    query: "fingerprint:b2a8:validator-locale, window:18m",
    timeWindow: "2026-08-08 15:35:00 to 15:53:00 UTC",
    hash: "sha256:65f1c43c...e09a",
    collector: "incident-aggregator@0.8.0",
    citedBy: "Impact: 47 occurrences across two backend hosts in an 18 minute window.",
    log: `fingerprint=b2a8:validator-locale\noccurrences=47\nhosts=VM-6-17-tencentos, VM-6-21-tencentos\nfirst_seen=2026-08-08T15:35:12.109Z\nlast_seen=2026-08-08T15:52:14.750Z`,
  },
} as const;

export const remediationRationale = {
  facts: [
    "EV-104 shows locale fr reaches validator lookup before a registered instance is confirmed.",
    "EV-106 confirms the failure repeats on two hosts, so it is not a host-local process state.",
  ],
  codeContext: "production@4f9c2b7 initializes only en and zh validators; the HTTP error path accepts normalized request locales without a fallback guard.",
  alternatives: [
    ["Register fr globally", "Not selected", "Changes the supported-language contract without evidence that fr content is deployed."],
    ["Return a 4xx for unknown locale", "Not selected", "Avoids a 500 but changes client-visible behavior and leaves the fallback policy unused."],
    ["Fallback before lookup", "Selected", "Matches the configured default-locale policy and keeps the request path available."],
  ],
  comparison: {
    originalRevision: "production@4f9c2b7",
    modifiedRevision: "hotfix/locale-fallback",
    files: [
      {
        path: "internal/validator/locale.go",
        additions: 5,
        deletions: 1,
        rows: [
          {
            original: { line: 42, kind: "context", content: "func (r Registry) ForLocale(locale string) Validator {" },
            modified: { line: 42, kind: "context", content: "func (r Registry) ForLocale(locale string) Validator {" },
          },
          {
            original: { line: 43, kind: "removed", content: "    return r.validators[normalize(locale)]" },
            modified: { line: 43, kind: "added", content: "    normalized := normalize(locale)" },
          },
          {
            original: null,
            modified: { line: 44, kind: "added", content: "    if validator, ok := r.validators[normalized]; ok {" },
          },
          {
            original: null,
            modified: { line: 45, kind: "added", content: "        return validator" },
          },
          {
            original: null,
            modified: { line: 46, kind: "added", content: "    }" },
          },
          {
            original: null,
            modified: { line: 47, kind: "added", content: "    return r.validators[r.defaultLocale]" },
          },
          {
            original: { line: 44, kind: "context", content: "}" },
            modified: { line: 48, kind: "context", content: "}" },
          },
        ],
      },
      {
        path: "internal/validator/locale_test.go",
        additions: 5,
        deletions: 0,
        rows: [
          {
            original: { line: 12, kind: "context", content: "func TestRegistryForLocale(t *testing.T) {" },
            modified: { line: 12, kind: "context", content: "func TestRegistryForLocale(t *testing.T) {" },
          },
          {
            original: { line: 13, kind: "context", content: "    registry := newTestRegistry()" },
            modified: { line: 13, kind: "context", content: "    registry := newTestRegistry()" },
          },
          {
            original: null,
            modified: { line: 14, kind: "added", content: "    validator := registry.ForLocale(\"fr\")" },
          },
          {
            original: null,
            modified: { line: 15, kind: "added", content: "    if validator != registry.ForLocale(\"en\") {" },
          },
          {
            original: null,
            modified: { line: 16, kind: "added", content: "        t.Fatal(\"expected configured default validator\")" },
          },
          {
            original: null,
            modified: { line: 17, kind: "added", content: "    }" },
          },
          {
            original: null,
            modified: { line: 18, kind: "added", content: "" },
          },
          {
            original: { line: 14, kind: "context", content: "}" },
            modified: { line: 19, kind: "context", content: "}" },
          },
        ],
      },
    ],
    get file() { return this.files[0].path; },
    get rows() { return this.files[0].rows; },
  },
  command: "go test ./internal/validator/... -run LocaleFallback -count=1",
};

export const scmProviders = {
  yunxiao: {
    label: "Yunxiao",
    repository: "real-estate-api",
    releaseSource: "Yunxiao release record",
    deploymentHint: "Optional release and draft-PR metadata integration",
  },
  github: {
    label: "GitHub Enterprise",
    repository: "platform / real-estate-api",
    releaseSource: "GitHub deployment record",
    deploymentHint: "Optional deployment and draft-PR metadata integration",
  },
  gitlab: {
    label: "GitLab",
    repository: "platform / real-estate-api",
    releaseSource: "GitLab environment deployment record",
    deploymentHint: "Optional environment deployment and merge-request metadata integration",
  },
  generic: {
    label: "Generic HTTPS Git",
    repository: "https://git.example.internal/platform/real-estate-api.git",
    releaseSource: "Configured deployment metadata source",
    deploymentHint: "Configured deployment metadata source, if available",
  },
} as const;

export const setupDefaults = {
  repository: "yunxiao / real-estate-api",
  productionBranch: "production",
  deployedCommit: "4f9c2b7",
  release: "2026.08.08.3",
  evidenceScope: "CLS error logs and 5 minute read-only context",
  credential: "yunxiao-prod-change (restricted: real-estate-api, hotfix/*)",
};

export const remediation = {
  productionBaseline: "production@4f9c2b7",
  branch: "hotfix/INC-2048-validator-fr",
  draftPr: "PR #841 - Guard unsupported validator locales",
  patch: "Normalize unsupported locales to the configured default before validator lookup.",
  tests: "18 passed, 0 failed - locale fallback suite",
  mergeGate: "Human approval required for production merge",
};

export const auditEvents = [
  ["15:53:10", "system", "Created INC-2048 from new error fingerprint"],
  ["15:53:11", "system", "Collected CLS context under read-only scope"],
  ["15:54:03", "lin", "Viewed incident evidence"],
  ["15:55:41", "lin", "Updated notification policy metadata"],
  ["16:02:08", "AI remediation", "Resolved production@4f9c2b7 and generated a patch"],
  ["16:03:16", "Yunxiao", "Created hotfix/INC-2048-validator-fr and draft PR #841"],
  ["16:03:17", "system", "Production merge is awaiting human approval"],
] as const;
