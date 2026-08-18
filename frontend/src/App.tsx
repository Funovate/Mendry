import { useEffect, useMemo, useState, type ChangeEvent, type ReactNode } from "react";
import {
  Activity, ArrowRight, Bell, BookOpenCheck, Check, CheckCircle2, ChevronLeft,
  ChevronsUpDown, CircleHelp, CircleAlert, ClipboardCheck, Code2, Eye,
  FileSearch, Filter, Gauge, GitBranch, GitCompareArrows, GitPullRequest,
  KeyRound, ListFilter, LockKeyhole, Menu, MoreHorizontal, PanelLeftClose,
  RefreshCw, Settings2, ShieldCheck, ShieldAlert, SlidersHorizontal,
  UserRound, X,
} from "lucide-react";
import {
  auditEvents, evidence, evidenceDetails, incidents, remediation,
  remediationRationale, scmProviders, setupDefaults, type Incident, type Role, validatorIncident,
} from "./data";

type View = "incidents" | "events" | "sources" | "policies" | "audit" | "setup";
type DetailTab = "Overview" | "Evidence" | "Report" | "Remediation" | "Activity";
type EvidenceId = keyof typeof evidenceDetails;
type ScmProviderId = keyof typeof scmProviders;
type EvidenceSourceKind = "mcp" | "api" | "ssh" | "webhook";
type LogSourceKind = "ssh" | "cloud" | "mcp";
type TriggerKind = "webhook" | "custom";
type KeyValueEntry = { key: string; value: string };
type McpTransport = "stdio" | "streamable-http" | "sse";

const logSourceOptions: Array<{ value: LogSourceKind; label: string; detail: string }> = [
  { value: "ssh", label: "SSH logs", detail: "Read-only tail and journal context from a selected host" },
  { value: "cloud", label: "Cloud logs", detail: "Approved read-only queries against a cloud log project" },
  { value: "mcp", label: "MCP", detail: "Search logs and surrounding context through an MCP server" },
];

const triggerModeOptions: Array<{ value: TriggerKind; label: string; detail: string }> = [
  { value: "webhook", label: "Webhook", detail: "Accept signed alerts from a monitoring or cloud provider" },
  { value: "custom", label: "Custom rule", detail: "Define a local fingerprint, threshold, and grouping rule" },
];

const defaultMcpJson = JSON.stringify({
  mcpServers: {
    cls: {
      type: "streamable-http",
      url: "https://cls-mcp.internal",
    },
  },
}, null, 2);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function recordToKeyValueEntries(value: unknown): KeyValueEntry[] {
  if (!isRecord(value)) return [{ key: "", value: "" }];
  const entries = Object.entries(value).filter(([, item]) => ["string", "number", "boolean"].includes(typeof item)).map(([key, item]) => ({ key, value: String(item) }));
  return entries.length > 0 ? entries : [{ key: "", value: "" }];
}

const navItems: Array<{ id: Exclude<View, "setup">; label: string; icon: typeof Activity }> = [
  { id: "incidents", label: "Incidents", icon: Activity },
  { id: "events", label: "Event stream", icon: ListFilter },
  { id: "sources", label: "Sources", icon: Settings2 },
  { id: "policies", label: "Notification policies", icon: Bell },
  { id: "audit", label: "Audit", icon: ShieldCheck },
];

function StatusPill({ value }: { value: string }) {
  const tone = value === "Open" ? "open" : value === "Recovered" ? "recovered" : value === "Closed" ? "closed" : value === "P2" ? "p2" : "info";
  return <span className={`status-pill ${tone}`}>{value}</span>;
}

function IconButton({ label, children, onClick }: { label: string; children: ReactNode; onClick?: () => void }) {
  return <button className="icon-button" type="button" aria-label={label} title={label} onClick={onClick}>{children}</button>;
}

function Trend({ count }: { count: number }) {
  const heights = count > 100 ? [28, 44, 36, 67, 54, 78, 42, 58] : [22, 30, 18, 43, 35, 58, 45, 70];
  return <div className="trend" aria-label={`${count} occurrences trend`}>{heights.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}</div>;
}

function IncidentRow({ incident, selected, onSelect }: { incident: Incident; selected: boolean; onSelect: () => void }) {
  return <button type="button" className={`incident-row ${selected ? "selected" : ""}`} onClick={onSelect}>
    <div className="row-topline"><span className="incident-id">{incident.id}</span><span className="time">{incident.lastSeen}</span></div>
    <strong>{incident.title}</strong>
    <div className="row-meta"><StatusPill value={incident.status} /><StatusPill value={incident.priority} />{incident.muted && <span className="muted">Muted</span>}<span>{incident.count} events</span></div>
    <Trend count={incident.count} />
  </button>;
}

function SummaryBlock({ label, value, note }: { label: string; value: string; note: string }) {
  return <div className="summary-block"><span>{label}</span><strong>{value}</strong><small>{note}</small></div>;
}

function EvidenceDrawer({ evidenceId, onClose, onShowReport }: { evidenceId: EvidenceId; onClose: () => void; onShowReport: () => void }) {
  const item = evidenceDetails[evidenceId];
  const [replayed, setReplayed] = useState(false);
  return <div className="drawer-layer" role="dialog" aria-modal="true" aria-labelledby="evidence-title">
    <button className="drawer-backdrop" aria-label="Dismiss evidence overlay" onClick={onClose} />
    <aside className="evidence-drawer">
      <header className="drawer-header"><div><div className="eyebrow">Evidence item {item.id}</div><h2 id="evidence-title">{item.title}</h2><p>{item.source}</p></div><IconButton label="Close evidence" onClick={onClose}><X size={19} /></IconButton></header>
      <section className="trust-banner"><ShieldCheck size={18} /><div><strong>Read-only evidence</strong><span>Content is redacted before display and linked to its collected source record.</span></div></section>
      <section className="drawer-section"><div className="section-label"><FileSearch size={15} />Original log excerpt</div><pre>{item.log}</pre></section>
      <section className="drawer-section evidence-metadata"><div><span>Query</span><code>{item.query}</code></div><div><span>Time window</span><strong>{item.timeWindow}</strong></div><div><span>Integrity</span><code>{item.hash}</code></div><div><span>Collector</span><strong>{item.collector}</strong></div></section>
      <section className="drawer-section citation-card"><div className="section-label"><ClipboardCheck size={15} />Used in diagnosis</div><p>{item.citedBy}</p><button className="text-button" type="button" onClick={onShowReport}>View cited conclusion <ArrowRight size={14} /></button></section>
      <footer className="drawer-footer"><button className="secondary-button" type="button" onClick={() => setReplayed(true)}><RefreshCw size={16} />Re-run read-only query</button>{replayed && <span className="query-result"><CheckCircle2 size={15} />Fixture result matches stored hash</span>}</footer>
    </aside>
  </div>;
}

function EvidencePanel({ onInspect, onOpenReport }: { onInspect: (id: EvidenceId) => void; onOpenReport: () => void }) {
  return <section className="tab-panel evidence-panel"><div className="panel-heading"><div><div className="eyebrow">Reviewable source material</div><h2>Evidence chain</h2><p>Open an item to inspect its redacted source text, query, time window, and integrity record.</p></div><button className="secondary-button" type="button" onClick={() => onInspect("EV-104")}><Eye size={16} />Inspect raw evidence</button></div>
    <div className="evidence-list">{evidence.map((item) => <article key={item.id} className="evidence-row"><div className="evidence-marker"><FileSearch size={17} /></div><div><div className="evidence-title"><strong>{item.type}</strong><span>{item.id}</span></div><p>{item.detail}</p><span>{item.source}</span></div><dl><dt>Scope</dt><dd>{item.scope}</dd><dt>Collected</dt><dd>{item.collected}</dd><dt>Retention</dt><dd>{item.retained}</dd></dl><button className="row-action" type="button" onClick={() => onInspect(item.id as EvidenceId)}>Inspect <ArrowRight size={14} /></button></article>)}</div>
    <section className="trace-strip"><div><ShieldCheck size={18} /><span><strong>Audit-ready chain</strong> Every conclusion links to evidence ID, source query, collector version, and retained hash.</span></div><button className="text-button" type="button" onClick={onOpenReport}>Review conclusions <ArrowRight size={14} /></button></section>
  </section>;
}

function ReportPanel({ onInspect }: { onInspect: (id: EvidenceId) => void }) {
  return <section className="report-panel"><div className="panel-heading"><div><div className="eyebrow">Deterministic report + AI proposal</div><h2>What is known, inferred, and still missing</h2><p>Evidence-backed facts are separate from the model's remediation proposal.</p></div><span className="confidence-pill">Medium confidence</span></div>
    <div className="report-grid"><article className="report-section facts"><div><span>Observed fact</span><h3>Locale resolution reaches an unregistered validator instance.</h3></div><p>Raw log records <code>fr</code> at lookup and no matching validator. This is directly observed.</p><button className="text-button" type="button" onClick={() => onInspect("EV-104")}>Inspect EV-104 <ArrowRight size={14} /></button></article><article className="report-section hypothesis"><div><span>Working hypothesis</span><h3>Request locale can bypass the configured fallback.</h3></div><p>Production code context and repeated logs support this, but request origin is not yet attributable.</p><button className="text-button" type="button" onClick={() => onInspect("EV-106")}>Inspect EV-106 <ArrowRight size={14} /></button></article><article className="report-section missing"><div><span>Missing evidence</span><h3>Request path and locale origin</h3></div><p>Request IDs are absent. The system will not state that a client is the cause until gateway evidence is collected.</p></article><article className="report-section remediation"><div><span>Proposed remediation</span><h3>Fallback before validator lookup.</h3></div><p>The choice is linked to the production baseline and a configured default-locale policy; it does not extend language support.</p></article></div>
  </section>;
}

function SideBySideDiffViewer() {
  const [selectedFileIndex, setSelectedFileIndex] = useState(0);
  const comparison = remediationRationale.comparison;
  const selectedFile = comparison.files[selectedFileIndex];

  return <section className="diff-panel"><div className="section-label"><Code2 size={16} />Scoped patch from immutable baseline</div><div className="diff-workspace"><div className="diff-file-list" role="tablist" aria-label="Changed files">{comparison.files.map((file, index) => <button type="button" role="tab" aria-selected={index === selectedFileIndex} className={index === selectedFileIndex ? "active" : ""} onClick={() => setSelectedFileIndex(index)} key={file.path}><code>{file.path}</code><span><b>+{file.additions}</b>{file.deletions > 0 && <i>-{file.deletions}</i>}</span></button>)}</div><div className="diff-comparison" aria-label="Side-by-side patch comparison">{(["original", "modified"] as const).map((side) => { const isOriginal = side === "original"; const title = isOriginal ? "Original" : "Modified"; const revision = isOriginal ? comparison.originalRevision : comparison.modifiedRevision; return <section className={`diff-pane ${side}`} aria-label={`${title} code`} key={side}><header className="diff-pane-header"><h3>{title}</h3><code>{revision}</code></header><div className="diff-file"><code>{selectedFile.path}</code></div><div className="diff-code-scroll"><div className="diff-lines">{selectedFile.rows.map((row, index) => { const line = row[side]; return <div className={`diff-line ${line?.kind ?? "empty"}`} key={index}><code className="diff-line-number">{line?.line ?? ""}</code><code className="diff-line-content">{line?.content ?? ""}</code></div>; })}</div></div></section>; })}</div></div></section>;
}

function RemediationPanel({ onInspect }: { onInspect: (id: EvidenceId) => void }) {
  const [showDiff, setShowDiff] = useState(false);
  return <section className="remediation-panel"><div className="panel-heading"><div><div className="eyebrow">AI remediation package</div><h2>Draft PR ready for human merge approval</h2><p>The automated path stops at a restricted hotfix branch and draft PR. The proposed patch remains reviewable.</p></div><button className="secondary-button" type="button"><GitPullRequest size={16} />Open draft PR</button></div>
    <div className="remediation-status"><div><span>Production baseline</span><strong>{remediation.productionBaseline}</strong><small>Resolved from deployed release {setupDefaults.release}</small></div><div><span>Hotfix branch</span><strong>{remediation.branch}</strong><small>Created automatically; no operator approval required</small></div><div><span>Draft PR</span><strong>{remediation.draftPr}</strong><small>Contains evidence citations and verification criteria</small></div></div>
    <div className="reasoning-layout"><section className="reasoning-flow"><div className="section-label"><GitCompareArrows size={16} />Why this change</div><div className="reason-step"><span>1</span><div><strong>Evidence-backed facts</strong><ul>{remediationRationale.facts.map((fact, index) => <li key={fact}><button type="button" onClick={() => onInspect(index === 0 ? "EV-104" : "EV-106")}>{fact.split(" ")[0]}</button>{fact.slice(6)}</li>)}</ul></div></div><div className="reason-step"><span>2</span><div><strong>Code context at production@4f9c2b7</strong><p>{remediationRationale.codeContext}</p></div></div><div className="reason-step selected"><span>3</span><div><strong>Selected change: fallback before lookup</strong><p>{remediation.patch}</p><button className="text-button" type="button" onClick={() => setShowDiff((value) => !value)}><Code2 size={14} />{showDiff ? "Hide patch diff" : "Inspect patch diff"}</button></div></div></section>
      <section className="alternatives"><div className="section-label"><CircleAlert size={16} />Alternatives considered</div>{remediationRationale.alternatives.map(([option, state, reason]) => <div className="alternative" key={option}><div><strong>{option}</strong><span className={state === "Selected" ? "decision selected" : "decision"}>{state}</span></div><p>{reason}</p></div>)}</section></div>
    {showDiff && <SideBySideDiffViewer />}
    <section className="verification-panel"><div><div className="section-label"><CheckCircle2 size={16} />Verification</div><strong>{remediation.tests}</strong><code>{remediationRationale.command}</code><p>Runs in an isolated workspace against the pinned baseline; it does not touch production.</p></div><div className="merge-gate"><div className="remediation-icon warning"><ShieldAlert size={18} /></div><div><span>Production merge gate</span><h3>{remediation.mergeGate}</h3><p>The system cannot merge, deploy, or claim recovery. Human approval and post-release evidence are still required.</p></div></div></section>
  </section>;
}

function SignalConfiguration() {
  const [logSource, setLogSource] = useState<LogSourceKind>("mcp");
  const [triggerMode, setTriggerMode] = useState<TriggerKind>("webhook");
  const [, setVerified] = useState(false);
  return <><div className="setup-card-title"><Activity size={20} /><div><h2>Evidence and trigger policy</h2><p>Choose one read-only log source and one trigger mode for this project. The selected paths are normalized into one incident flow.</p></div></div><EvidenceCompositionPanel logSource={logSource} triggerMode={triggerMode} onLogSourceChange={(source) => { setLogSource(source); setVerified(false); }} onTriggerModeChange={(trigger) => { setTriggerMode(trigger); setVerified(false); }} onVerified={setVerified} /><div className="choice-note"><ShieldCheck size={16} />Git remains the code foundation. One log source and one trigger mode are selected and deduplicated.</div></>;
}

function KeyValueEditor({ label, rows, onChange, secret = false, addLabel }: { label: string; rows: KeyValueEntry[]; onChange: (rows: KeyValueEntry[]) => void; secret?: boolean; addLabel: string }) {
  const updateRow = (index: number, field: keyof KeyValueEntry, value: string) => onChange(rows.map((row, rowIndex) => rowIndex === index ? { ...row, [field]: value } : row));
  const removeRow = (index: number) => onChange(rows.filter((_, rowIndex) => rowIndex !== index));
  return <div className="key-value-editor"><div className="section-label"><Settings2 size={15} />{label}</div><div className="key-value-header"><span>Key</span><span>Value</span></div>{rows.map((row, index) => <div className="key-value-row" key={`${label}-${index}`}><input aria-label={`${label} key ${index + 1}`} value={row.key} onChange={(event) => updateRow(index, "key", event.target.value)} placeholder="KEY" /><input aria-label={`${label} value ${index + 1}`} type={secret ? "password" : "text"} value={row.value} onChange={(event) => updateRow(index, "value", event.target.value)} placeholder="Value" /><IconButton label={`Remove ${label} ${index + 1}`} onClick={() => removeRow(index)}><X size={15} /></IconButton></div>)}<button className="text-button" type="button" onClick={() => onChange([...rows, { key: "", value: "" }])}>+ {addLabel}</button></div>;
}

function McpEvidenceSourceFields() {
  const [mode, setMode] = useState<"studio" | "json">("studio");
  const [serverName, setServerName] = useState("cls");
  const [transport, setTransport] = useState<McpTransport>("streamable-http");
  const [endpoint, setEndpoint] = useState("https://cls-mcp.internal");
  const [command, setCommand] = useState("npx");
  const [argumentsValue, setArgumentsValue] = useState("--service\nreal-estate-api\n--environment\nproduction");
  const [workingDirectory, setWorkingDirectory] = useState("/srv/real-estate");
  const [headers, setHeaders] = useState<KeyValueEntry[]>([{ key: "", value: "" }]);
  const [variables, setVariables] = useState<KeyValueEntry[]>([{ key: "CLS_PROJECT", value: "real-estate-prod" }, { key: "CLS_LOGSTORE", value: "server-log" }]);
  const [parameters, setParameters] = useState<KeyValueEntry[]>([{ key: "service", value: "real-estate-api" }, { key: "environment", value: "production" }]);
  const [toolScope, setToolScope] = useState(["search_logs", "get_context"]);
  const [connectionTested, setConnectionTested] = useState(false);
  const [jsonConfig, setJsonConfig] = useState(defaultMcpJson);
  const [jsonError, setJsonError] = useState("");
  const [jsonMessage, setJsonMessage] = useState("");

  const toggleTool = (tool: string) => setToolScope((current) => current.includes(tool) ? current.filter((item) => item !== tool) : [...current, tool]);

  const applyJson = (text: string) => {
    try {
      const parsed: unknown = JSON.parse(text);
      if (!isRecord(parsed)) throw new Error("MCP JSON must be an object");

      const servers: Array<[string, unknown]> = isRecord(parsed.mcpServers)
        ? Object.entries(parsed.mcpServers).filter(([, value]) => isRecord(value))
        : [["imported-server", parsed]];
      if (servers.length === 0) throw new Error("No MCP server configuration found");
      if (servers.length > 1) throw new Error("This source accepts one MCP server. Import a configuration containing exactly one server.");

      const [name, configValue] = servers[0];
      if (!isRecord(configValue)) throw new Error("MCP server configuration must be an object");
      const config = configValue;
      const hasUrl = typeof config.url === "string" && config.url.trim().length > 0;
      const hasCommand = typeof config.command === "string" && config.command.trim().length > 0;
      if (hasUrl === hasCommand) throw new Error("Configure exactly one connection target: url for HTTP, or command for stdio.");

      const importedType = typeof config.type === "string" ? config.type.toLowerCase() : undefined;
      if (hasCommand && importedType && importedType !== "stdio") throw new Error("A command-based MCP server must use the stdio transport.");
      if (hasUrl && importedType && !["http", "streamable-http", "sse"].includes(importedType)) {
        throw new Error("Unsupported remote MCP transport: " + importedType);
      }

      setServerName(name);
      if (hasUrl) {
        setTransport(importedType === "sse" ? "sse" : "streamable-http");
        setEndpoint(config.url as string);
        setHeaders(recordToKeyValueEntries(config.headers));
        setVariables([{ key: "", value: "" }]);
      } else {
        setTransport("stdio");
        setCommand(config.command as string);
        setHeaders([{ key: "", value: "" }]);
        setVariables(recordToKeyValueEntries(config.env));
        setArgumentsValue(Array.isArray(config.args) ? config.args.filter((item): item is string => typeof item === "string").join("\n") : "");
        if (typeof config.cwd === "string") setWorkingDirectory(config.cwd);
      }
      setConnectionTested(false);
      setJsonError("");
      setJsonMessage("Loaded " + name + " from JSON. Review the Studio fields before saving.");
      setMode("studio");
    } catch (error) {
      setJsonMessage("");
      setJsonError(error instanceof Error ? error.message : "Invalid MCP JSON");
    }
  };

  const importJsonFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    const text = await file.text();
    setJsonConfig(text);
    applyJson(text);
    event.target.value = "";
  };

  const isRemote = transport !== "stdio";
  const canTest = serverName.trim().length > 0 && (isRemote ? endpoint.trim().length > 0 : command.trim().length > 0);

  return <>
    <div className="setup-card-title">
      <FileSearch size={20} />
      <div>
        <h2>MCP source connection</h2>
        <p>Configure one MCP server explicitly, or import a common client <code>mcpServers</code> JSON shape and review the parsed values.</p>
      </div>
    </div>
    <div className="mcp-mode-switch" role="group" aria-label="MCP configuration mode">
      <button type="button" className={mode === "studio" ? "active" : ""} onClick={() => setMode("studio")}>MCP Studio</button>
      <button type="button" className={mode === "json" ? "active" : ""} onClick={() => setMode("json")}>Import JSON</button>
    </div>
    {mode === "json" ? <section className="json-import-panel">
      <label>MCP JSON configuration<textarea aria-label="MCP JSON configuration" value={jsonConfig} onChange={(event) => setJsonConfig(event.target.value)} /></label>
      <div className="file-actions">
        <label className="secondary-button file-button"><FileSearch size={16} />Import JSON file<input aria-label="MCP JSON file" type="file" accept=".json,application/json" onChange={importJsonFile} /></label>
        <button className="primary-button" type="button" onClick={() => applyJson(jsonConfig)}><Check size={16} />Load JSON into Studio</button>
      </div>
      {jsonError && <div className="validation-error"><ShieldAlert size={16} />{jsonError}</div>}
      <div className="choice-note"><ShieldCheck size={16} />Accepts exactly one server using a remote <code>url</code> or a local <code>command</code>. Imported secrets remain masked for review.</div>
    </section> : <>
      <div className="source-form">
        <label>Server name<input aria-label="MCP server name" value={serverName} onChange={(event) => { setServerName(event.target.value); setConnectionTested(false); }} /></label>
        <label>Transport<select aria-label="MCP transport" value={transport} onChange={(event) => { setTransport(event.target.value as McpTransport); setConnectionTested(false); }}>
          <option value="stdio">Local stdio</option>
          <option value="streamable-http">Streamable HTTP (recommended)</option>
          <option value="sse">Legacy HTTP + SSE</option>
        </select></label>
      </div>
      {isRemote ? <>
        <label>MCP endpoint URL<input aria-label="MCP server URL" type="url" value={endpoint} onChange={(event) => { setEndpoint(event.target.value); setConnectionTested(false); }} placeholder="https://example.com/mcp" /></label>
        {transport === "sse" && <div className="choice-note"><ShieldAlert size={16} />Legacy compatibility mode for MCP servers using the deprecated HTTP + SSE transport.</div>}
        <KeyValueEditor label="Additional request headers" rows={headers} onChange={(rows) => { setHeaders(rows); setConnectionTested(false); }} secret addLabel="Add request header" />
      </> : <>
        <div className="source-form">
          <label>Command<input aria-label="MCP command" value={command} onChange={(event) => { setCommand(event.target.value); setConnectionTested(false); }} /></label>
          <label>Working directory (optional)<input aria-label="MCP working directory" value={workingDirectory} onChange={(event) => { setWorkingDirectory(event.target.value); setConnectionTested(false); }} /></label>
        </div>
        <label>Arguments (one per line)<textarea aria-label="MCP arguments" value={argumentsValue} onChange={(event) => { setArgumentsValue(event.target.value); setConnectionTested(false); }} placeholder={"--project\nreal-estate-prod"} /></label>
        <KeyValueEditor label="Process environment variables" rows={variables} onChange={(rows) => { setVariables(rows); setConnectionTested(false); }} addLabel="Add environment variable" />
      </>}
      <div className="source-form">
        <label>Credential reference<select aria-label="MCP credential reference" defaultValue="cls-mcp-prod"><option value="cls-mcp-prod">cls-mcp-prod · read-only</option><option value="cloud-logs-readonly">cloud-logs-readonly · read-only</option></select></label>
        <label>Evidence profile<select aria-label="MCP evidence profile" defaultValue="errors-context"><option value="errors-context">Errors with five-minute context</option><option value="errors-only">Errors only</option><option value="logs-metrics">Logs plus service metrics</option></select></label>
      </div>
      <KeyValueEditor label="Evidence query scope" rows={parameters} onChange={setParameters} addLabel="Add scope filter" />
      <div className="mcp-connection-check">
        <button className="secondary-button" type="button" disabled={!canTest} onClick={() => setConnectionTested(true)}><RefreshCw size={16} />Test connection and discover tools</button>
        {connectionTested && <div className="validation-success"><CheckCircle2 size={16} />MCP initialization succeeded. 3 tools discovered from this server.</div>}
      </div>
      <div>
        <div className="section-label"><Eye size={15} />Tool permissions</div>
        <div className="trigger-options">
          <button type="button" className={toolScope.includes("search_logs") ? "trigger-option enabled" : "trigger-option"} aria-pressed={toolScope.includes("search_logs")} onClick={() => toggleTool("search_logs")}><span>{toolScope.includes("search_logs") ? <Check size={14} /> : "+"}</span><div><strong>Search logs</strong><small>Query the connected log service within the selected scope.</small></div></button>
          <button type="button" className={toolScope.includes("get_context") ? "trigger-option enabled" : "trigger-option"} aria-pressed={toolScope.includes("get_context")} onClick={() => toggleTool("get_context")}><span>{toolScope.includes("get_context") ? <Check size={14} /> : "+"}</span><div><strong>Get surrounding context</strong><small>Read nearby events around a detected error.</small></div></button>
          <button type="button" className={toolScope.includes("get_metrics") ? "trigger-option enabled" : "trigger-option"} aria-pressed={toolScope.includes("get_metrics")} onClick={() => toggleTool("get_metrics")}><span>{toolScope.includes("get_metrics") ? <Check size={14} /> : "+"}</span><div><strong>Get service metrics</strong><small>Read error rate and latency context when the server exposes it.</small></div></button>
        </div>
      </div>
      {jsonMessage && <div className="validation-success"><CheckCircle2 size={16} />{jsonMessage}</div>}
    </>}
  </>;
}

function ApiEvidenceSourceFields() {
  return <><div className="setup-card-title"><FileSearch size={20} /><div><h2>Cloud log API connection</h2><p>Choose the cloud log account and approved query scope used by the evidence collector.</p></div></div><div className="source-form"><label>Cloud log provider<select aria-label="Cloud log provider" defaultValue="tencent-cls"><option value="tencent-cls">Tencent CLS</option><option value="aliyun-sls">Alibaba Cloud SLS</option><option value="aws-cloudwatch">AWS CloudWatch Logs</option></select></label><label>Cloud region<select aria-label="Cloud region" defaultValue="ap-guangzhou"><option value="ap-guangzhou">ap-guangzhou</option><option value="ap-shanghai">ap-shanghai</option><option value="cn-hangzhou">cn-hangzhou</option></select></label></div><label>API endpoint<input aria-label="API endpoint" defaultValue="https://logs.cloud.example/v1" /></label><div className="source-form"><label>Project or account<select aria-label="Log project or account" defaultValue="real-estate-prod"><option value="real-estate-prod">real-estate-prod</option><option value="shared-platform-prod">shared-platform-prod</option></select></label><label>Log store or group<select aria-label="Log store or group" defaultValue="server-log"><option value="server-log">server-log</option><option value="access-log">access-log</option><option value="audit-log">audit-log</option></select></label></div><div className="source-form"><label>Credential reference<select aria-label="API credential reference" defaultValue="cls-api-readonly"><option value="cls-api-readonly">cls-api-readonly · read-only</option><option value="cloud-logs-readonly">cloud-logs-readonly · read-only</option></select></label><label>Query preset<select aria-label="Log query preset" defaultValue="errors-context"><option value="errors-context">Errors with five-minute context</option><option value="errors-only">Error level only</option><option value="service-deployments">Service and deployment events</option></select></label></div><div className="choice-note"><ShieldCheck size={16} />The collector uses an approved query preset. Credentials are referenced by name and never exposed in this form.</div></>;
}

function SshEvidenceSourceFields() {
  const [authMethod, setAuthMethod] = useState<"key" | "password">("key");
  return <><div className="setup-card-title"><FileSearch size={20} /><div><h2>SSH log collection</h2><p>Connect to a concrete host, choose the authentication method, and restrict collection to the project files you select.</p></div></div><div className="source-form"><label>SSH host<input aria-label="SSH host" defaultValue="api-prod-01.internal" /></label><label>SSH port<input aria-label="SSH port" type="number" defaultValue="22" min="1" max="65535" /></label></div><div className="source-form"><label>Connection method<select aria-label="SSH connection method" value={authMethod} onChange={(event) => setAuthMethod(event.target.value as "key" | "password")}><option value="key">SSH private key</option><option value="password">Password</option></select></label><label>SSH username<input aria-label="SSH username" defaultValue="deploy-readonly" /></label></div>{authMethod === "key" ? <><label>SSH private key<textarea aria-label="SSH private key" placeholder="Paste an OpenSSH private key. Stored encrypted and never displayed again." /></label><label>Key passphrase (optional)<input aria-label="SSH key passphrase" type="password" autoComplete="off" placeholder="Optional passphrase" /></label></> : <label>SSH password<input aria-label="SSH password" type="password" autoComplete="off" placeholder="Stored encrypted and never displayed again" /></label>}<div className="source-form"><label>Project folder<input aria-label="Project folder" defaultValue="/srv/real-estate" /></label><label>Log path<input aria-label="SSH log path" defaultValue="/var/log/real-estate/server.log" /></label></div><label>Collection mode<select aria-label="SSH collection mode" defaultValue="errors-context"><option value="errors-context">Errors with five-minute context</option><option value="errors-only">Error lines only</option><option value="service-status">Service status and recent journal</option></select></label><div className="inferred-field"><span>Restricted command scope</span><strong>Read-only tail and journal queries</strong><small>Commands run through the selected host. Shell access, file writes, and arbitrary commands are unavailable.</small></div></>;
}

function WebhookEvidenceSourceFields() {
  const [eventTypes, setEventTypes] = useState(["alarm-fired", "alarm-recovered"]);
  const [webhookUrl, setWebhookUrl] = useState("https://fixthe.internal/hooks/real-estate/production");
  const toggleEvent = (eventType: string) => setEventTypes((current) => current.includes(eventType) ? current.filter((item) => item !== eventType) : [...current, eventType]);

  return <><div className="setup-card-title"><FileSearch size={20} /><div><h2>Signed alert webhook</h2><p>Configure the receiving URL and verify every payload before it enters the evidence chain.</p></div></div><label>Webhook URL<input aria-label="Webhook URL" value={webhookUrl} onChange={(event) => setWebhookUrl(event.target.value)} placeholder="https://alerts.example.com/hooks/real-estate" /></label><div className="source-form"><label>Signing credential<select aria-label="Webhook signing credential" defaultValue="alarm-signing-prod"><option value="alarm-signing-prod">alarm-signing-prod · HMAC</option><option value="cloud-alerts-signing">cloud-alerts-signing · HMAC</option></select></label><label>Event environment<select aria-label="Webhook event environment" defaultValue="production"><option value="production">production</option><option value="staging">staging</option></select></label></div><div><div className="section-label"><Bell size={15} />Accepted event types</div><div className="trigger-options"><button type="button" className={eventTypes.includes("alarm-fired") ? "trigger-option enabled" : "trigger-option"} aria-pressed={eventTypes.includes("alarm-fired")} onClick={() => toggleEvent("alarm-fired")}><span>{eventTypes.includes("alarm-fired") ? <Check size={14} /> : "+"}</span><div><strong>Alarm firing</strong><small>Open or enrich an incident when an external alarm enters a firing state.</small></div></button><button type="button" className={eventTypes.includes("alarm-recovered") ? "trigger-option enabled" : "trigger-option"} aria-pressed={eventTypes.includes("alarm-recovered")} onClick={() => toggleEvent("alarm-recovered")}><span>{eventTypes.includes("alarm-recovered") ? <Check size={14} /> : "+"}</span><div><strong>Alarm recovery</strong><small>Match a recovery event to the existing fingerprint and close its lifecycle.</small></div></button><button type="button" className={eventTypes.includes("deployment-health") ? "trigger-option enabled" : "trigger-option"} aria-pressed={eventTypes.includes("deployment-health")} onClick={() => toggleEvent("deployment-health")}><span>{eventTypes.includes("deployment-health") ? <Check size={14} /> : "+"}</span><div><strong>Deployment health</strong><small>Keep release and deployment signals in the same evidence chain.</small></div></button></div></div><label>Deduplication key<select aria-label="Webhook deduplication key" defaultValue="fingerprint"><option value="fingerprint">Alert fingerprint</option><option value="alert-id">External alert ID</option><option value="service-alert">Service plus alert name</option></select></label></>;
}

function EvidenceSourceFields({ source }: { source: EvidenceSourceKind }) {
  switch (source) {
    case "mcp": return <McpEvidenceSourceFields />;
    case "api": return <ApiEvidenceSourceFields />;
    case "ssh": return <SshEvidenceSourceFields />;
    case "webhook": return <WebhookEvidenceSourceFields />;
  }
}

type CompositionSelectorProps = {
  logSource: LogSourceKind | null;
  triggerMode: TriggerKind | null;
  onLogSourceChange: (source: LogSourceKind) => void;
  onTriggerModeChange: (trigger: TriggerKind) => void;
  };

function CompositionSelector({ logSource, triggerMode, onLogSourceChange, onTriggerModeChange }: CompositionSelectorProps) {
  return <div className="composition-selector"><section className="composition-group"><div className="section-label"><FileSearch size={15} />Log source <small>Choose one</small></div><div className="composition-options" role="radiogroup" aria-label="Project log source">{logSourceOptions.map((option) => { const selected = logSource === option.value; return <label className={`composition-option ${selected ? "selected" : ""}`} key={option.value}><input type="radio" name="project-log-source" aria-label={`Log source ${option.label}`} checked={selected} onChange={() => onLogSourceChange(option.value)} /><span className="composition-check" aria-hidden="true">{selected && <Check size={13} />}</span><span><strong>{option.label}</strong><small>{option.detail}</small></span></label>; })}</div></section><section className="composition-group"><div className="section-label"><Bell size={15} />Trigger mode <small>Choose one</small></div><div className="composition-options" role="radiogroup" aria-label="Project trigger mode">{triggerModeOptions.map((option) => { const selected = triggerMode === option.value; return <label className={`composition-option ${selected ? "selected" : ""}`} key={option.value}><input type="radio" name="project-trigger-mode" aria-label={`Trigger ${option.label}`} checked={selected} onChange={() => onTriggerModeChange(option.value)} /><span className="composition-check" aria-hidden="true">{selected && <Check size={13} />}</span><span><strong>{option.label}</strong><small>{option.detail}</small></span></label>; })}</div></section></div>;
}

function CustomRuleTriggerFields() {
  return <section className="custom-rule-fields"><div className="setup-card-title"><SlidersHorizontal size={20} /><div><h2>Custom incident rule</h2><p>Define the event pattern and grouping window that opens an incident without requiring a provider webhook.</p></div></div><div className="source-form"><label>Rule name<input aria-label="Custom rule name" defaultValue="Unhandled backend errors" /></label><label>Grouping window<select aria-label="Custom rule grouping window" defaultValue="15"><option value="5">5 minutes</option><option value="15">15 minutes</option><option value="30">30 minutes</option></select></label></div><label>Match expression<textarea aria-label="Custom rule match expression" defaultValue={'level=ERROR service="checkout-backend"'} /></label><div className="choice-note"><ShieldCheck size={16} />Rules run against normalized events and deduplicate with webhook-triggered incidents using the same fingerprint.</div></section>;
}

function EvidenceCompositionPanel({ logSource, triggerMode, onLogSourceChange, onTriggerModeChange, onVerified }: CompositionSelectorProps & { onVerified: (verified: boolean) => void }) {
  const [verified, setVerified] = useState(false);
  const changeLogSource = (source: LogSourceKind) => {
    setVerified(false);
    onVerified(false);
    onLogSourceChange(source);
  };
  const changeTriggerMode = (trigger: TriggerKind) => {
    setVerified(false);
    onVerified(false);
    onTriggerModeChange(trigger);
  };
  const verify = () => {
    if (!logSource || !triggerMode) return;
    setVerified(true);
    onVerified(true);
  };
  return <section className="composition-panel"><CompositionSelector logSource={logSource} triggerMode={triggerMode} onLogSourceChange={changeLogSource} onTriggerModeChange={changeTriggerMode} /><div className="composition-configurations"><div className="section-label"><Settings2 size={15} />Configure selected integration</div>{logSource ? <section className="composition-source" key={logSource}><div className="composition-source-header"><div><strong>{logSourceOptions.find((option) => option.value === logSource)?.label}</strong><small>Read-only evidence connection</small></div><span className="source-state">Selected</span></div><EvidenceSourceFields source={logSource === "cloud" ? "api" : logSource} /></section> : <div className="validation-error"><CircleAlert size={16} />Select one log source before verification.</div>}</div><div className="composition-triggers"><div className="section-label"><SlidersHorizontal size={15} />Configure selected trigger</div>{triggerMode === "webhook" && <section className="composition-trigger"><WebhookEvidenceSourceFields /></section>}{triggerMode === "custom" && <section className="composition-trigger"><CustomRuleTriggerFields /></section>}{!triggerMode && <div className="validation-error"><CircleAlert size={16} />Select one trigger mode before verification.</div>}</div>{verified ? <div className="validation-success"><CheckCircle2 size={16} />Selected log source and trigger mode are ready for this project.</div> : <button className="secondary-button" type="button" disabled={!logSource || !triggerMode} onClick={verify}><CheckCircle2 size={16} />Verify selected composition</button>}</section>;
}

function EvidencePolicyEditPanel({ onDone }: { onDone: () => void }) {
  const [logSource, setLogSource] = useState<LogSourceKind>("mcp");
  const [triggerMode, setTriggerMode] = useState<TriggerKind>("webhook");
  const [verified, setVerified] = useState(false);

  return <ConfigurationEditFrame title="Edit Evidence and trigger policy" description="Choose one read-only SSH, cloud, or MCP log source and one webhook or custom-rule trigger." onDone={onDone}><section className="quick-edit"><EvidenceCompositionPanel logSource={logSource} triggerMode={triggerMode} onLogSourceChange={setLogSource} onTriggerModeChange={setTriggerMode} onVerified={setVerified} /><div className="choice-note"><ShieldCheck size={16} />{verified ? "The selected composition is ready and will be recorded in audit history." : "Verify the selected composition before saving this policy."}</div><ConfigurationEditFooter onDone={onDone} disabled={!verified} /></section></ConfigurationEditFrame>;
}

function ConfigurationEditFrame({ title, description, onDone, children }: { title: string; description: string; onDone: () => void; children: ReactNode }) {
  return <section className="setup-view"><div className="setup-header"><button type="button" className="back-link" onClick={onDone}><ChevronLeft size={17} />Configuration overview</button><div className="eyebrow">Configuration edit</div><h1>{title}</h1><p>{description}</p></div>{children}</section>;
}

function ConfigurationEditFooter({ onDone, disabled = false }: { onDone: () => void; disabled?: boolean }) {
  return <footer><button className="secondary-button" type="button" onClick={onDone}>Cancel</button><button className="primary-button" type="button" disabled={disabled} onClick={onDone}><Check size={16} />Save changes</button></footer>;
}

function GitRemoteEditPanel({ onDone }: { onDone: () => void }) {
  const [remoteUrl, setRemoteUrl] = useState("https://git.example.internal/platform/real-estate-api.git");
  const [providerId, setProviderId] = useState<ScmProviderId>("yunxiao");
  const [fetchVerified, setFetchVerified] = useState(false);
  const provider = scmProviders[providerId];
  const canSave = remoteUrl.trim().length > 0;

  return <ConfigurationEditFrame title="Edit Git remote" description="Update the repository URL and optional deployment metadata in one step." onDone={onDone}><section className="quick-edit"><div className="setup-card-title"><GitBranch size={20} /><div><h2>Git remote and metadata</h2><p>Git transport uses this remote for clone, fetch, branch, and restricted hotfix pushes.</p></div></div><label>Git remote URL<input aria-label="Git remote URL" value={remoteUrl} onChange={(event) => { setRemoteUrl(event.target.value); setFetchVerified(false); }} /></label><label>SCM metadata provider<select aria-label="SCM metadata provider" value={providerId} onChange={(event) => { setProviderId(event.target.value as ScmProviderId); setFetchVerified(false); }}><option value="yunxiao">Yunxiao</option><option value="github">GitHub Enterprise</option><option value="gitlab">GitLab</option><option value="generic">No provider API / generic Git</option></select></label><div className="inferred-field"><span>Metadata integration</span><strong>{provider.releaseSource}</strong><small>{provider.deploymentHint}</small></div>{fetchVerified ? <div className="validation-success"><CheckCircle2 size={16} />Remote fetch verified and metadata is reachable.</div> : <button className="secondary-button" type="button" disabled={!canSave} onClick={() => setFetchVerified(true)}><CheckCircle2 size={16} />Test fetch</button>}<div className="choice-note"><ShieldCheck size={16} />Only the remote and its metadata provider are changed. Transport credentials remain separate.</div><ConfigurationEditFooter onDone={onDone} disabled={!canSave} /></section></ConfigurationEditFrame>;
}

function GitCredentialEditPanel({ onDone }: { onDone: () => void }) {
  const [authMethod, setAuthMethod] = useState<"http" | "ssh">("http");
  const [gitUsername, setGitUsername] = useState("git-bot");
  const [secret, setSecret] = useState("");
  const [credentialVerified, setCredentialVerified] = useState(false);
  const canSave = secret.trim().length >= 8 && (authMethod === "ssh" || gitUsername.trim().length > 0);

  const updateSecret = (value: string) => {
    setSecret(value);
    setCredentialVerified(false);
  };

  return <ConfigurationEditFrame title="Edit Git transport credential" description="Choose the transport method and provide the new credential directly in this form." onDone={onDone}><section className="quick-edit"><div className="setup-card-title"><KeyRound size={20} /><div><h2>Git transport credential</h2><p>Credentials are encrypted on save, never displayed again, and limited to the configured repository and hotfix branch.</p></div></div><label>Authentication method<select aria-label="Git authentication method" value={authMethod} onChange={(event) => { setAuthMethod(event.target.value as "http" | "ssh"); setCredentialVerified(false); setSecret(""); }}><option value="http">HTTP(S) username and password/token</option><option value="ssh">SSH private key and optional passphrase</option></select></label>{authMethod === "http" ? <><label>Git username<input aria-label="Git username" value={gitUsername} onChange={(event) => { setGitUsername(event.target.value); setCredentialVerified(false); }} /></label><label className="secret-input"><span>Git HTTP password or token</span><input aria-label="Git HTTP password" type="password" autoComplete="off" value={secret} onChange={(event) => updateSecret(event.target.value)} placeholder="Stored encrypted; never displayed again" /></label></> : <label className="secret-input"><span>SSH private key</span><textarea aria-label="SSH private key" value={secret} onChange={(event) => updateSecret(event.target.value)} placeholder="Paste private key; stored encrypted and never displayed again" /></label>}<div className="permission-grid"><div><Eye size={16} /><strong>Git read scope</strong><small>Clone and fetch from the configured remote</small></div><div><GitBranch size={16} /><strong>Git write scope</strong><small>Push only <code>hotfix/*</code> to the configured remote</small></div></div>{credentialVerified ? <div className="validation-success"><CheckCircle2 size={16} />Credential accepted for the configured Git scope.</div> : <button className="secondary-button" type="button" disabled={!canSave} onClick={() => setCredentialVerified(true)}><LockKeyhole size={16} />Test credential</button>}<div className="choice-note"><ShieldAlert size={16} />The existing secret stays hidden. Saving this form replaces the encrypted transport credential.</div><ConfigurationEditFooter onDone={onDone} disabled={!canSave} /></section></ConfigurationEditFrame>;
}

function ProductionBaselineEditPanel({ onDone }: { onDone: () => void }) {
  const [branch, setBranch] = useState(setupDefaults.productionBranch);
  const isProductionBranch = branch === setupDefaults.productionBranch;

  return <ConfigurationEditFrame title="Edit Production baseline" description="Select the branch used to resolve and pin the deployed commit for remediation." onDone={onDone}><section className="quick-edit"><div className="setup-card-title"><ShieldCheck size={20} /><div><h2>Production baseline</h2><p>The selected branch is resolved to a deployed commit when the configuration is saved.</p></div></div><label>Production branch<select aria-label="Production branch" value={branch} onChange={(event) => setBranch(event.target.value)}><option value="production">production</option><option value="release/2026.08">release/2026.08</option></select></label><div className="baseline-card"><span>{scmProviders.yunxiao.releaseSource}</span><strong>{isProductionBranch ? setupDefaults.release : "Release metadata"}</strong><p>Yunxiao metadata resolves <code>{branch}@{isProductionBranch ? setupDefaults.deployedCommit : "deployed commit"}</code></p><div className="validation-success"><CheckCircle2 size={16} />The selected branch will be pinned after save.</div></div><div className="choice-note"><ShieldCheck size={16} />Only the production branch and its resolved baseline change. The immutable commit is used as the remediation reference.</div><ConfigurationEditFooter onDone={onDone} /></section></ConfigurationEditFrame>;
}

function QuickEditPanel({ target, onDone }: { target: number; onDone: () => void }) {
  if (target === 3) return <EvidencePolicyEditPanel onDone={onDone} />;
  if (target === 0) return <GitRemoteEditPanel onDone={onDone} />;
  if (target === 1) return <GitCredentialEditPanel onDone={onDone} />;
  if (target === 2) return <ProductionBaselineEditPanel onDone={onDone} />;
  const panels = [
    ["Git remote", "Git remote URL", "https://git.example.internal/platform/real-estate-api.git", "Optional SCM metadata provider", "Yunxiao release record"],
    ["Git transport credential", "Credential reference", "git-http-prod", "Permission", "fetch + push hotfix/* only"],
    ["Production baseline", "Production branch", "production", "Pinned deployed commit", "4f9c2b7"],
    ["Evidence and trigger policy", "Log sources", "SSH / cloud / MCP", "Trigger modes", "Webhook / custom rules"],
  ] as const;
  const panel = panels[target] ?? panels[0];
  return <section className="setup-view"><div className="setup-header"><button type="button" className="back-link" onClick={onDone}><ChevronLeft size={17} />Configuration overview</button><div className="eyebrow">Local edit</div><h1>Edit {panel[0]}</h1><p>Only this configuration is changed and validated. Other production context remains untouched.</p></div><section className="quick-edit"><label>{panel[1]}<input aria-label={panel[1]} defaultValue={panel[2]} /></label><label>{panel[3]}<input aria-label={panel[3]} defaultValue={panel[4]} /></label><div className="choice-note"><ShieldCheck size={16} />Save validates this item only and records the change in audit history.</div><footer><button className="secondary-button" type="button" onClick={onDone}>Cancel</button><button className="primary-button" type="button" onClick={onDone}><Check size={16} />Save changes</button></footer></section></section>;
}

type CreatedProject = {
  name: string;
  environment: string;
  baselineBranch: string;
  scope: string;
  logSource: LogSourceKind;
  triggerMode: TriggerKind;
};

function ProjectCreatedState({ project, onBack, onOpenProject }: { project: CreatedProject; onBack: () => void; onOpenProject?: (project: CreatedProject) => void }) {
  const logSourceSummary = logSourceOptions.find((option) => option.value === project.logSource)?.label;
  const triggerSummary = triggerModeOptions.find((option) => option.value === project.triggerMode)?.label;
  return <section className="setup-view"><div className="setup-header"><div className="eyebrow">Project created</div><h1>{project.name} is ready</h1><p>The project has a pinned production context and a read-only evidence path. Review or replace individual integrations from Sources.</p></div><section className="project-created"><div className="created-banner"><CheckCircle2 size={23} /><div><strong>Configuration saved locally</strong><span>{project.name} / {project.environment} can now receive incident observations.</span></div></div><div className="created-summary"><div><span>Project</span><strong>{project.name}</strong></div><div><span>Environment</span><strong>{project.environment}</strong></div><div><span>Production baseline</span><strong>{project.baselineBranch}@{setupDefaults.deployedCommit}</strong></div></div><div className="created-checks"><div><CheckCircle2 size={17} /><span><strong>Repository connected</strong><small>Transport credential stored as a hidden reference.</small></span></div><div><CheckCircle2 size={17} /><span><strong>{logSourceSummary} enabled</strong><small>{project.scope}; read-only collection is ready.</small></span></div><div><CheckCircle2 size={17} /><span><strong>{triggerSummary} enabled</strong><small>Selected paths deduplicate into one incident flow.</small></span></div><div><ShieldAlert size={17} /><span><strong>Production merge remains gated</strong><small>Only a human can approve a production-branch merge.</small></span></div></div><footer><button className="secondary-button" type="button" onClick={onBack}>Back to projects</button>{onOpenProject ? <button className="primary-button" type="button" onClick={() => onOpenProject(project)}>Open project <ArrowRight size={16} /></button> : <button className="primary-button" type="button" onClick={onBack}>Back to configuration <ArrowRight size={16} /></button>}</footer></section></section>;
}

function CreateProjectWizard({ role, onBack, onOpenProject }: { role: Role; onBack: () => void; onOpenProject?: (project: CreatedProject) => void }) {
  const [step, setStep] = useState(0);
  const [created, setCreated] = useState(false);
  const [projectName, setProjectName] = useState("checkout-api");
  const [environment, setEnvironment] = useState("production");
  const [serviceName, setServiceName] = useState("checkout-backend");
  const [providerId, setProviderId] = useState<ScmProviderId>("yunxiao");
  const [remoteUrl, setRemoteUrl] = useState("https://git.example.internal/platform/checkout-api.git");
  const [authMethod, setAuthMethod] = useState<"http" | "ssh">("http");
  const [gitUsername, setGitUsername] = useState("git-bot");
  const [branch, setBranch] = useState("production");
  const [scope, setScope] = useState("Five minute context around errors");
  const [logSource, setLogSource] = useState<LogSourceKind | null>(null);
  const [triggerMode, setTriggerMode] = useState<TriggerKind | null>(null);
  const [secret, setSecret] = useState("");
  const [credentialsSaved, setCredentialsSaved] = useState(false);
  const [baselineVerified, setBaselineVerified] = useState(false);
  const [evidenceVerified, setEvidenceVerified] = useState(false);
  const provider = scmProviders[providerId];
  const steps = ["Project details", "Repository", "Credentials", "Production baseline", "Evidence scope", "Review"];
  const details = ["Git required + choose integrations", "Git remote and metadata", "Write-only transport secret", "Pinned deployed commit", "Composable evidence and triggers", "Enable after final review"];
  const slug = projectName.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "project-slug";
  const canSaveCredential = secret.trim().length >= 8 && (authMethod === "ssh" || gitUsername.trim().length > 0);
  const canContinue = step === 0
    ? projectName.trim().length >= 2 && environment.trim().length > 0 && serviceName.trim().length > 0 && logSource !== null && triggerMode !== null
    : step === 1
      ? remoteUrl.trim().length > 0
      : step === 2
        ? credentialsSaved
        : step === 3
          ? baselineVerified
          : step === 4
            ? evidenceVerified
            : true;
  const saveCredential = () => {
    if (!canSaveCredential) return;
    setCredentialsSaved(true);
    setSecret("");
  };
  const createProject = () => setCreated(true);

  if (role !== "admin") return <section className="setup-view"><div className="setup-header"><button type="button" className="back-link" onClick={onBack}><ChevronLeft size={17} />Projects</button><div className="eyebrow">New project</div><h1>Create project</h1></div><section className="access-denied"><LockKeyhole size={24} /><h2>Administrator access required</h2><p>Only administrators can create a project or store its repository credential reference.</p></section></section>;
  if (created) {
    if (!logSource || !triggerMode) return null;
    return <ProjectCreatedState project={{ name: projectName.trim(), environment: environment.trim(), baselineBranch: branch, scope, logSource, triggerMode }} onBack={onBack} onOpenProject={onOpenProject} />;
  }

  return <section className="setup-view create-project-view"><div className="setup-header"><button type="button" className="back-link" onClick={onBack}><ChevronLeft size={17} />Projects</button><div className="eyebrow">New project</div><h1>Create project</h1><p>Configure the project identity, production code context, evidence access, and final safety boundary before it can receive incidents.</p></div><div className="setup-layout"><aside className="setup-steps" aria-label="Create project steps">{steps.map((label, index) => <div className={index === step ? "active" : index < step ? "complete" : ""} key={label}><span>{index < step ? <Check size={14} /> : index + 1}</span><strong>{label}</strong><small>{details[index]}</small></div>)}</aside><section className="setup-card create-project-card">
    {step === 0 && <><div className="setup-card-title"><Settings2 size={20} /><div><h2>Define project identity</h2><p>Git is required for every project. Choose one log source and one trigger mode for this service.</p></div></div><div className="source-form"><label>Project name<input aria-label="New project name" value={projectName} onChange={(event) => setProjectName(event.target.value)} placeholder="e.g. checkout-api" /></label><label>Production environment<input aria-label="New project environment" value={environment} onChange={(event) => setEnvironment(event.target.value)} placeholder="production" /></label></div><label>Primary service<input aria-label="Primary service" value={serviceName} onChange={(event) => setServiceName(event.target.value)} placeholder="checkout-backend" /></label><section className="required-integration"><GitBranch size={18} /><div><strong>Git repository</strong><small>Required foundation for production baseline lookup and scoped remediation branches.</small></div><span>Required</span></section><CompositionSelector logSource={logSource} triggerMode={triggerMode} onLogSourceChange={setLogSource} onTriggerModeChange={setTriggerMode} /><div className="inferred-field"><span>Project key</span><strong>{slug}</strong><small>Used to group incidents and scope evidence. It can be changed before creation.</small></div><div className="choice-note"><ShieldCheck size={16} />Select one log source and one trigger mode. Their specific configuration appears in the Evidence scope step.</div></>}
    {step === 1 && <><div className="setup-card-title"><GitBranch size={20} /><div><h2>Connect source control</h2><p>Git transport is independent from optional deployment and draft-PR metadata. The selected remote is the only repository scope.</p></div></div><label>Git remote URL<input aria-label="Create Git remote URL" value={remoteUrl} onChange={(event) => setRemoteUrl(event.target.value)} /></label><label>SCM provider<select aria-label="Create SCM provider" value={providerId} onChange={(event) => setProviderId(event.target.value as ScmProviderId)}><option value="yunxiao">Yunxiao</option><option value="github">GitHub Enterprise</option><option value="gitlab">GitLab</option><option value="generic">No provider API / generic Git</option></select></label><div className="inferred-field"><span>Optional deployment metadata</span><strong>{provider.releaseSource}</strong><small>{provider.deploymentHint}. Clone, fetch, branch, and restricted hotfix pushes use the transport credential in the next step.</small></div><div className="choice-note"><ShieldCheck size={16} />A provider API is optional for Git transport. No provider token is requested in this flow.</div></>}
    {step === 2 && <><div className="setup-card-title"><KeyRound size={20} /><div><h2>Store Git transport credential</h2><p>Use a write-only transport reference for the selected repository. The entered value is cleared immediately after local save.</p></div></div>{credentialsSaved ? <div className="credential-saved"><CheckCircle2 size={21} /><div><strong>Credential saved as git-{authMethod}-prod</strong><span>The secret value is hidden and cannot be retrieved from the prototype.</span></div><button className="secondary-button" type="button" onClick={() => { setCredentialsSaved(false); setSecret(""); }}>Replace credential</button></div> : <><label>Authentication method<select aria-label="Create Git authentication method" value={authMethod} onChange={(event) => { setAuthMethod(event.target.value as "http" | "ssh"); setCredentialsSaved(false); setSecret(""); }}><option value="http">HTTP(S) username and password/token</option><option value="ssh">SSH private key and optional passphrase</option></select></label>{authMethod === "http" ? <><label>Git username<input aria-label="Create Git username" value={gitUsername} onChange={(event) => setGitUsername(event.target.value)} /></label><label className="secret-input"><span>Git HTTP password or token</span><input aria-label="Create Git HTTP password" type="password" autoComplete="off" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder="Stored encrypted; never displayed again" /></label></> : <label className="secret-input"><span>SSH private key</span><textarea aria-label="Create SSH private key" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder="Paste private key; stored encrypted and never displayed again" /></label>}<button className="secondary-button" type="button" disabled={!canSaveCredential} onClick={saveCredential}><LockKeyhole size={16} />Save encrypted credential</button></>}<div className="permission-grid"><div><Eye size={16} /><strong>Git read scope</strong><small>Clone and fetch from {slug}</small></div><div><GitBranch size={16} /><strong>Git write scope</strong><small>Push only <code>hotfix/*</code>; merge and deploy are unavailable</small></div></div><div className="choice-note"><ShieldAlert size={16} />This credential cannot approve a production merge, deploy a release, or modify production data.</div></>}
    {step === 3 && <><div className="setup-card-title"><ShieldCheck size={20} /><div><h2>Pin the deployed production baseline</h2><p>Select the branch used for release lookup. Creation is blocked until its deployed commit is verified and pinned.</p></div></div><label>Production branch<select aria-label="Create production branch" value={branch} onChange={(event) => { setBranch(event.target.value); setBaselineVerified(false); }}><option value="production">production</option><option value="release/2026.08">release/2026.08</option></select></label><div className="baseline-card"><span>{provider.releaseSource}</span><strong>{setupDefaults.release}</strong><p>{provider.label} metadata resolves <code>{branch}@{setupDefaults.deployedCommit}</code></p>{baselineVerified ? <div className="validation-success"><CheckCircle2 size={16} />Deployed commit verified and pinned</div> : <button className="secondary-button" type="button" onClick={() => setBaselineVerified(true)}><ShieldCheck size={16} />Verify deployed commit</button>}</div><div className="choice-note"><ShieldCheck size={16} />The branch is only a lookup input. Remediation and review use the immutable deployed commit, never a moving branch name.</div></>}
    {step === 4 && <><div className="setup-card-title"><Eye size={20} /><div><h2>Configure evidence and trigger</h2><p>Keep collection read-only, configure the selected source and trigger, then verify the pair before review.</p></div></div><div className="scope-options" role="radiogroup" aria-label="Create project evidence scope">{["Five minute context around errors", "Errors only", "Errors plus service metrics"].map((option) => <label className={scope === option ? "selected" : ""} key={option}><input type="radio" name="create-evidence-scope" value={option} checked={scope === option} onChange={() => setScope(option)} /><span><strong>{option}</strong><small>{option === "Errors only" ? "Smallest access scope; surrounding context is not collected." : option === "Errors plus service metrics" ? "Adds read-only error rate and latency context when available." : "Recommended default for diagnosis and evidence review."}</small></span></label>)}</div><EvidenceCompositionPanel logSource={logSource} triggerMode={triggerMode} onLogSourceChange={setLogSource} onTriggerModeChange={setTriggerMode} onVerified={setEvidenceVerified} /><div className="choice-note"><ShieldCheck size={16} />Git remains the required code context. One selected log source and one selected trigger mode are verified into the incident flow.</div></>}
    {step === 5 && <><div className="setup-card-title"><ClipboardCheck size={20} /><div><h2>Review before enabling</h2><p>Confirm the project boundary and selected integrations. Creation stores local references only and never contacts a customer system.</p></div></div><div className="review-list create-review-list"><div><span>Project</span><strong>{projectName || "Not set"}</strong></div><div><span>Environment / service</span><strong>{environment || "Not set"} / {serviceName || "Not set"}</strong></div><div><span>Project key</span><strong>{slug}</strong></div><div><span>Git remote</span><strong>{remoteUrl || "Not set"}</strong></div><div><span>SCM metadata</span><strong>{provider.label} <small>optional</small></strong></div><div><span>Transport credential</span><strong>git-{authMethod}-prod <small>encrypted value hidden</small></strong></div><div><span>Baseline</span><strong>{branch}@{setupDefaults.deployedCommit} <small>immutable</small></strong></div><div><span>Log source</span><strong>{logSourceOptions.find((option) => option.value === logSource)?.label}</strong></div><div><span>Evidence scope</span><strong>{scope}</strong></div><div><span>Trigger mode</span><strong>{triggerModeOptions.find((option) => option.value === triggerMode)?.label}</strong></div></div><div className="readiness-panel"><div><CheckCircle2 size={16} /><span><strong>Ready to create</strong><small>All required references are present and locally verified.</small></span></div><div><ShieldAlert size={16} /><span><strong>Production action boundary</strong><small>Merge, deploy, and recovery claims remain human-only.</small></span></div></div></>}
    <footer className="setup-footer"><button className="secondary-button" type="button" disabled={step === 0} onClick={() => setStep((value) => Math.max(value - 1, 0))}>Back</button>{step < steps.length - 1 ? <button className="primary-button" type="button" disabled={!canContinue} onClick={() => setStep((value) => Math.min(value + 1, steps.length - 1))}>{step === 2 ? "Continue after saving" : step === 3 ? "Continue after verification" : step === 4 ? "Continue to review" : "Continue"}<ArrowRight size={16} /></button> : <button className="primary-button" type="button" onClick={createProject}><Check size={16} />Create project</button>}</footer>
  </section></div></section>;
}

function ProjectDirectory({ role, onBack, onOpenProject }: { role: Role; onBack: () => void; onOpenProject: (project: CreatedProject) => void }) {
  const [creating, setCreating] = useState(false);
  if (creating) return <CreateProjectWizard role={role} onBack={() => setCreating(false)} onOpenProject={onOpenProject} />;
  return <section className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>Switch or create a project</h1><p>Projects own required Git context, selectable log sources, trigger modes, and access boundaries.</p></div><button className="secondary-button" type="button" onClick={onBack}>Back to current project</button></header><div className="project-list"><button type="button" className="project-row active"><span className="project-initial">R</span><span><strong>real-estate</strong><small>production · 4 configured components</small></span><CheckCircle2 size={18} /></button><button type="button" className="project-row"><span className="project-initial muted">P</span><span><strong>payments</strong><small>staging · 2 evidence sources</small></span><ArrowRight size={18} /></button><button type="button" className="create-project-row" onClick={() => setCreating(true)}><span>+</span><div><strong>Create project</strong><small>Start with Git, then combine only the log sources and trigger modes this service needs.</small></div><ArrowRight size={18} /></button></div></section>;
}

function IncidentDetail({ incident, role, onBack }: { incident: Incident; role: Role; onBack: () => void }) {
  const [tab, setTab] = useState<DetailTab>("Overview");
  const [outcome, setOutcome] = useState(false);
  const [evidenceId, setEvidenceId] = useState<EvidenceId | null>(null);
  const canRecordOutcome = role !== "viewer";
  const inspect = (id: EvidenceId) => setEvidenceId(id);
  const showReport = () => { setEvidenceId(null); setTab("Report"); };
  return <section className="detail" aria-label="Incident detail">
    <div className="detail-header"><IconButton label="Back to incidents" onClick={onBack}><ChevronLeft size={18} /></IconButton><div className="detail-heading"><div className="eyebrow">{incident.id} <span>Fingerprint {incident.fingerprint}</span></div><h1>{incident.title}</h1><div className="detail-meta"><StatusPill value={incident.status} /><StatusPill value={incident.priority} /><span>First seen Aug 08, 15:52 UTC</span><span>Last seen {incident.lastSeen}</span></div></div><div className="detail-actions"><IconButton label="More incident actions"><MoreHorizontal size={19} /></IconButton>{canRecordOutcome && <button type="button" className="secondary-button" onClick={() => setOutcome(true)}><BookOpenCheck size={16} />{outcome ? "Outcome recorded" : "Record outcome"}</button>}<button type="button" className="primary-button" onClick={() => setTab("Remediation")}><GitBranch size={16} />View hotfix draft</button></div></div>
    <div className="tabs" role="tablist">{(["Overview", "Evidence", "Report", "Remediation", "Activity"] as DetailTab[]).map((item) => <button key={item} type="button" role="tab" aria-selected={tab === item} className={tab === item ? "active" : ""} onClick={() => setTab(item)}>{item}{item === "Evidence" && <span className="tab-count">3</span>}{item === "Remediation" && <span className="tab-count">1</span>}</button>)}</div>
    {tab === "Overview" && <div className="detail-grid"><div className="main-column"><section className="content-section"><div className="panel-heading"><div><h2>Observed facts</h2><p>Directly collected from read-only production evidence.</p></div><button className="secondary-button" type="button" onClick={() => inspect("EV-104")}><Eye size={16} />Inspect source log</button></div><div className="fact-callout"><code>validator instance for language 'fr' (normalized to 'fr') not registered</code><p>47 occurrences from two backend hosts. The first occurrence carries locale <code>fr</code>; no request ID was emitted by terminal error middleware.</p></div><div className="log-sample"><div><span>15:52:14.750 UTC</span><span>ERROR</span><span>VM-6-17-tencentos</span></div><code>http_encoder.go:79 · /app/run/real-estate/backend/api/logs/server.log</code></div></section><section className="content-section"><h2>Occurrence trend</h2><div className="large-trend"><Trend count={47} /><div className="chart-labels"><span>15:35</span><span>15:40</span><span>15:45</span><span>15:50</span><span>15:55</span></div></div></section></div><aside className="side-column"><section className="side-section"><h2>Impact</h2><SummaryBlock label="Occurrences" value="47" note="18 minute window" /><SummaryBlock label="Hosts" value="2" note="backend source" /><SummaryBlock label="Notification" value="No delivery" note="Info lifecycle default" /></section><section className="side-section"><h2>Collection</h2><dl><dt>Source</dt><dd>cls-backend</dd><dt>Environment</dt><dd>production</dd><dt>Scope</dt><dd>read-only CLS</dd><dt>Retention</dt><dd>30 days</dd></dl></section></aside></div>}
    {tab === "Evidence" && <EvidencePanel onInspect={inspect} onOpenReport={() => setTab("Report")} />}
    {tab === "Report" && <ReportPanel onInspect={inspect} />}
    {tab === "Remediation" && <RemediationPanel onInspect={inspect} />}
    {tab === "Activity" && <section className="tab-panel"><div className="panel-heading"><div><h2>Activity</h2><p>System and operator actions are append-only.</p></div></div><div className="activity-list">{auditEvents.map(([time, actor, text]) => <div key={`${time}-${text}`}><time>{time}</time><span className="actor">{actor}</span><p>{text}</p></div>)}</div></section>}
    {evidenceId && <EvidenceDrawer evidenceId={evidenceId} onClose={() => setEvidenceId(null)} onShowReport={showReport} />}
  </section>;
}

function SetupView({ role, onBack }: { role: Role; onBack: () => void }) {
  const [manage, setManage] = useState(true);
  const [creatingProject, setCreatingProject] = useState(false);
  const [step, setStep] = useState(0);
  const [providerId, setProviderId] = useState<ScmProviderId>("yunxiao");
  const [remoteUrl, setRemoteUrl] = useState("https://git.example.internal/platform/real-estate-api.git");
  const [authMethod, setAuthMethod] = useState<"http" | "ssh">("http");
  const [gitUsername, setGitUsername] = useState("git-bot");
  const [branch, setBranch] = useState(setupDefaults.productionBranch);
  const [scope] = useState("Five minute context around errors");
  const [secret, setSecret] = useState("");
  const [credentialsSaved, setCredentialsSaved] = useState(false);
  const [validated, setValidated] = useState(false);
  const provider = scmProviders[providerId];
  const steps = ["Repository", "Credentials", "Production baseline", "Evidence scope", "Review"];
  const details = ["Git remote and metadata", "Write-only Git transport secrets", "Pinned deployed commit", "Least-privilege read scope", "Review before enabling"];
  const next = () => setStep((value) => Math.min(value + 1, steps.length - 1));
  const saveCredentials = () => { if (secret.trim().length >= 8 && (authMethod === "ssh" || gitUsername.trim())) { setCredentialsSaved(true); setSecret(""); } };
  if (role !== "admin") return <section className="setup-view"><div className="setup-header"><button type="button" className="back-link" onClick={onBack}><ChevronLeft size={17} />Sources</button><div className="eyebrow">Project configuration</div><h1>Connect production context safely</h1></div><section className="access-denied"><LockKeyhole size={24} /><h2>Administrator access required</h2><p>Switch the prototype role to Admin to configure repositories and credential references.</p></section></section>;
  if (creatingProject) return <CreateProjectWizard role={role} onBack={() => setCreatingProject(false)} />;
  if (manage) return <section className="setup-view"><div className="setup-header"><button type="button" className="back-link" onClick={onBack}><ChevronLeft size={17} />Sources</button><div className="eyebrow">Project configuration</div><h1>Configuration overview</h1><p>Edit one integration or policy at a time. Saving an item verifies only that item and never restarts the setup flow.</p></div><section className="config-manager"><div className="manager-header"><div><strong>Production context is ready</strong><span>4 configured components, 0 action-required checks</span></div><button className="primary-button" type="button" onClick={() => setCreatingProject(true)}>Create project</button></div>{[[0, "Git remote", "HTTPS remote and optional SCM metadata", "Healthy"], [1, "Git transport credential", "git-http-prod, encrypted value hidden", "Healthy"], [2, "Production baseline", "production@4f9c2b7, immutable deployed revision", "Verified"], [3, "Evidence and trigger policy", "SSH, cloud, or MCP sources with webhook/custom rules", "Composable"]].map(([target, title, detail, state]) => <article key={title as string}><div><span>{state as string}</span><h2>{title as string}</h2><p>{detail as string}</p></div><button className="secondary-button" type="button" onClick={() => { setStep(target as number); setManage(false); }}>Edit</button></article>)}</section></section>;
  if (!manage) return <QuickEditPanel target={step} onDone={() => setManage(true)} />;
  return <section className="setup-view"><div className="setup-header"><button type="button" className="back-link" onClick={onBack}><ChevronLeft size={17} />Sources</button><div className="eyebrow">Project configuration</div><h1>Connect production context safely</h1><p>Guided choices keep provider configuration, secrets, deployed commits, and read permissions explicit without exposing sensitive values.</p></div><div className="setup-layout"><aside className="setup-steps">{steps.map((label, index) => <div className={index === step ? "active" : index < step ? "complete" : ""} key={label}><span>{index < step ? <Check size={14} /> : index + 1}</span><strong>{label}</strong><small>{details[index]}</small></div>)}</aside><section className="setup-card">
    {step === 0 && <><div className="setup-card-title"><GitBranch size={20} /><div><h2>Choose source control</h2><p>Git transport is configured independently from optional deployment and draft-PR metadata.</p></div></div><label>Git remote URL<input aria-label="Git remote URL" value={remoteUrl} onChange={(event) => { setRemoteUrl(event.target.value); setCredentialsSaved(false); }} /></label><label>Optional SCM metadata provider<select aria-label="SCM provider" value={providerId} onChange={(event) => { setProviderId(event.target.value as ScmProviderId); setValidated(false); }}><option value="yunxiao">Yunxiao</option><option value="github">GitHub Enterprise</option><option value="gitlab">GitLab</option><option value="generic">No provider API / generic Git</option></select></label><div className="inferred-field"><span>Metadata integration</span><strong>{provider.releaseSource}</strong><small>{provider.deploymentHint}. Git clone, fetch, branch, and push use the remote URL and transport credential below.</small></div></>}
    {step === 1 && <><div className="setup-card-title"><KeyRound size={20} /><div><h2>Store Git transport credential</h2><p>Choose how the Git remote authenticates. Values are encrypted on save, cleared from the form, and can only be replaced later.</p></div></div><label>Authentication method<select aria-label="Git authentication method" value={authMethod} onChange={(event) => { setAuthMethod(event.target.value as "http" | "ssh"); setCredentialsSaved(false); setSecret(""); }}><option value="http">HTTP(S) username and password/token</option><option value="ssh">SSH private key and optional passphrase</option></select></label>{authMethod === "http" ? <><label>Git username<input aria-label="Git username" value={gitUsername} onChange={(event) => { setGitUsername(event.target.value); setCredentialsSaved(false); }} /></label><label className="secret-input"><span>Git HTTP password or token</span><input aria-label="Git HTTP password" type="password" autoComplete="off" value={secret} onChange={(event) => { setSecret(event.target.value); setCredentialsSaved(false); }} placeholder="Stored encrypted; never displayed again" /></label></> : <label className="secret-input"><span>SSH private key</span><textarea aria-label="SSH private key" value={secret} onChange={(event) => { setSecret(event.target.value); setCredentialsSaved(false); }} placeholder="Paste private key; stored encrypted and never displayed again" /></label>}<div className="permission-grid"><div><Eye size={16} /><strong>Git read scope</strong><small>Clone and fetch from the configured remote</small></div><div><GitBranch size={16} /><strong>Git write scope</strong><small>Push only <code>hotfix/*</code> to the configured remote</small></div></div>{credentialsSaved ? <div className="validation-success"><CheckCircle2 size={16} />Git credential stored as <code>git-{authMethod}-prod</code>; its value is not retrievable.</div> : <button className="secondary-button" type="button" disabled={secret.trim().length < 8 || (authMethod === "http" && !gitUsername.trim())} onClick={saveCredentials}><LockKeyhole size={16} />Save encrypted Git credential</button>}<div className="choice-note"><ShieldAlert size={16} />A Git transport credential cannot merge, deploy, or modify production data.</div></>}
    {step === 2 && <><div className="setup-card-title"><ShieldCheck size={20} /><div><h2>Verify production baseline</h2><p>Select the moving branch once; the system resolves and pins the deployed commit for every remediation.</p></div></div><label>Production branch<select aria-label="Production branch" value={branch} onChange={(event) => { setBranch(event.target.value); setValidated(false); }}><option>production</option><option>release/2026.08</option></select></label><div className="baseline-card"><span>{provider.releaseSource}</span><strong>{setupDefaults.release}</strong><p>{provider.label} metadata resolves <code>{branch}@{setupDefaults.deployedCommit}</code></p>{validated ? <div className="validation-success"><CheckCircle2 size={16} />Commit is pinned and matches the deployed release</div> : <button className="secondary-button" type="button" onClick={() => setValidated(true)}><ShieldCheck size={16} />Verify deployed commit</button>}</div></>}
    {step === 3 && <SignalConfiguration />}
    {step === 4 && <><div className="setup-card-title"><ClipboardCheck size={20} /><div><h2>Review safe defaults</h2><p>This configuration enables analysis context only. It cannot merge, deploy, or change production.</p></div></div><div className="review-list"><div><span>Git remote</span><strong>{remoteUrl}</strong></div><div><span>Git credential</span><strong>{credentialsSaved ? `git-${authMethod}-prod, encrypted value hidden` : "not saved"}</strong></div><div><span>Metadata provider</span><strong>{provider.label} <small>optional</small></strong></div><div><span>Baseline</span><strong>{branch}@{setupDefaults.deployedCommit} <small>immutable</small></strong></div><div><span>Evidence</span><strong>{scope}</strong></div></div><div className="choice-note"><ShieldAlert size={16} />Production merge remains outside this system and requires human approval.</div></>}
    <footer className="setup-footer"><button className="secondary-button" type="button" disabled={step === 0} onClick={() => setStep((value) => value - 1)}>Back</button>{step < 4 ? <button className="primary-button" type="button" disabled={(step === 1 && !credentialsSaved) || (step === 2 && !validated)} onClick={next}>{step === 1 ? "Continue after saving" : step === 2 ? "Continue after verification" : "Continue"}<ArrowRight size={16} /></button> : <button className="primary-button" type="button" onClick={() => setStep(0)}><Check size={16} />Configuration ready</button>}</footer>
  </section></div></section>;
}

function SettingsView({ view, role, project, onConfigure }: { view: Exclude<View, "incidents" | "events" | "setup">; role: Role; project: CreatedProject; onConfigure: () => void }) {
  const content = view === "sources" ? { title: "Sources", subtitle: "Connector metadata and collection permissions", rows: [["cls-backend", "Tencent CLS MCP", "Pull + context + metrics", "Healthy"], ["yunxiao-release", "Yunxiao release API", "Baseline resolution", "Verified"], ["ingest-webhook", "Signed webhook", "Push ingestion", "Healthy"]] } : view === "policies" ? { title: "Notification policies", subtitle: "Rules affect delivery, never incident visibility", rows: [["Default lifecycle", "Project default", "First, escalation, recovery", "Enabled"], ["b2a8:validator-locale", "Fingerprint", "Lifecycle default", "Enabled"], ["6c43:consumer-retry", "Fingerprint", "Muted", "Muted"]] } : { title: "Audit", subtitle: "Configuration and evidence access are traceable", rows: auditEvents.map(([time, actor, text]) => [time, actor, text, "Recorded"]) };
  return <section className="settings-view"><div className="view-header"><div><div className="eyebrow">{project.name} / {project.environment}</div><h1>{content.title}</h1><p>{content.subtitle}</p></div>{role === "admin" && view === "sources" ? <button className="primary-button" type="button" onClick={onConfigure}><SlidersHorizontal size={16} />Configure project</button> : role === "admin" && view !== "audit" ? <button className="primary-button" type="button"><SlidersHorizontal size={16} />Manage metadata</button> : <span className="readonly-note">{role === "viewer" ? "Viewer access" : "Operator access"}</span>}</div><div className="setup-summary"><div><GitBranch size={17} /><span><strong>Production baseline</strong><small>{project.baselineBranch}@{setupDefaults.deployedCommit}, verified from release 2026.08.08.3</small></span></div><div><Eye size={17} /><span><strong>Evidence scope</strong><small>{project.scope}</small></span></div>{view === "sources" && role === "admin" && <button type="button" className="text-button" onClick={onConfigure}>Review configuration <ArrowRight size={14} /></button>}</div><div className="table-wrap"><table><thead><tr><th>Name / time</th><th>Type / actor</th><th>Scope / action</th><th>Status</th></tr></thead><tbody>{content.rows.map((row) => <tr key={row.join("-")}>{row.map((cell) => <td key={cell}>{cell}</td>)}</tr>)}</tbody></table></div>{view === "sources" && <section className="metadata-note"><CircleHelp size={17} /><p>Secret values are never displayed. Connectors use read-only source permissions; the separately scoped change credential is limited to configured repositories and <code>hotfix/*</code>.</p></section>}</section>;
}

function EventsView({ project }: { project: CreatedProject }) {
  return <section className="settings-view"><div className="view-header"><div><div className="eyebrow">{project.name} / {project.environment}</div><h1>Event stream</h1><p>All accepted observations remain visible, including repeated fingerprints.</p></div><button className="secondary-button" type="button"><Filter size={16} />Filter stream</button></div><div className="event-list">{["15:52:14.750 ERROR validator instance for language fr not registered", "15:51:49.403 ERROR validator instance for language fr not registered", "15:49:10.991 ERROR PostgreSQL pool acquisition timeout", "15:47:22.019 WARN RabbitMQ consumer retry scheduled"].map((event, index) => <article key={event}><span className={index === 3 ? "level warn" : "level error"}>{index === 3 ? "WARN" : "ERROR"}</span><code>{event}</code><span>cls-backend</span></article>)}</div></section>;
}

export default function App() {
  const [view, setView] = useState<View>("incidents");
  const [selected, setSelected] = useState<Incident | null>(validatorIncident);
  const [role, setRole] = useState<Role>("operator");
  const [statusFilter, setStatusFilter] = useState("All");
  const [mobileOpen, setMobileOpen] = useState(false);
  const [projectDirectory, setProjectDirectory] = useState(false);
  const [activeProject, setActiveProject] = useState<CreatedProject>({ name: "real-estate", environment: "production", baselineBranch: setupDefaults.productionBranch, scope: "CLS error logs + five-minute context, read-only", logSource: "mcp", triggerMode: "webhook" });
  useEffect(() => { const openDirectory = (event: MouseEvent) => { if ((event.target as Element | null)?.closest(".project-switcher")) { event.preventDefault(); event.stopPropagation(); setProjectDirectory(true); } }; document.addEventListener("click", openDirectory, true); return () => document.removeEventListener("click", openDirectory, true); }, []);
  const filtered = useMemo(() => incidents.filter((incident) => statusFilter === "All" || incident.status === statusFilter), [statusFilter]);
  const chooseView = (next: View) => { setView(next); setMobileOpen(false); };
  if (projectDirectory) return <ProjectDirectory role={role} onBack={() => setProjectDirectory(false)} onOpenProject={(project) => { setActiveProject(project); setProjectDirectory(false); setView("incidents"); setSelected(null); }} />;
  return <div className="app-shell"><aside className={`sidebar ${mobileOpen ? "mobile-open" : ""}`}><div className="brand"><div className="brand-mark"><Gauge size={20} /></div><span>fixthe</span><IconButton label="Close navigation" onClick={() => setMobileOpen(false)}><X size={18} /></IconButton></div><button type="button" className="project-switcher" onClick={() => chooseView("sources")}><span><small>Project</small><strong>{activeProject.name}</strong></span><ChevronsUpDown size={16} /></button><nav>{navItems.map(({ id, label, icon: Icon }) => <button type="button" className={view === id ? "nav-active" : ""} onClick={() => chooseView(id)} key={id}><Icon size={17} />{label}{id === "incidents" && <span className="nav-count">3</span>}</button>)}</nav><div className="sidebar-foot"><div className="environment"><span className="env-dot" />{activeProject.environment}</div><div className="user-card"><div className="avatar">L</div><div><strong>Lin Chen</strong><small>Operations</small></div></div></div></aside>{mobileOpen && <button type="button" className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}<main><header className="topbar"><div className="topbar-left"><IconButton label="Open navigation" onClick={() => setMobileOpen(true)}><Menu size={19} /></IconButton><div className="breadcrumbs"><span>{activeProject.name}</span><span>/</span><strong>{activeProject.environment}</strong>{view === "setup" && <><span>/</span><strong>project setup</strong></>}</div></div><div className="topbar-right"><label className="role-control"><UserRound size={15} /><span>Role</span><select value={role} onChange={(event) => setRole(event.target.value as Role)} aria-label="Prototype role"><option value="admin">Admin</option><option value="operator">Operator</option><option value="viewer">Viewer</option></select></label><IconButton label="Collapse details"><PanelLeftClose size={18} /></IconButton></div></header>{view === "setup" ? <SetupView role={role} onBack={() => chooseView("sources")} /> : view === "incidents" ? <div className="incidents-layout"><section className={`incident-list-pane ${selected ? "with-detail" : ""}`}><div className="list-header"><div><div className="eyebrow">Production incidents</div><h1>Incidents <span>{filtered.length}</span></h1></div><IconButton label="Filter incidents"><Filter size={18} /></IconButton></div><div className="filter-row"><select aria-label="Filter incident status" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option>All</option><option>Open</option><option>Recovered</option><option>Closed</option></select><button type="button" className="filter-chip"><SlidersHorizontal size={14} />Source: all</button></div><div className="incident-list">{filtered.map((incident) => <IncidentRow key={incident.id} incident={incident} selected={selected?.id === incident.id} onSelect={() => setSelected(incident)} />)}</div></section>{selected ? <IncidentDetail incident={selected} role={role} onBack={() => setSelected(null)} /> : <section className="empty-detail"><Activity size={28} /><h2>Select an incident</h2><p>Choose a row to inspect evidence and next actions.</p></section>}</div> : view === "events" ? <EventsView project={activeProject} /> : <SettingsView view={view} role={role} project={activeProject} onConfigure={() => chooseView("setup")} />}</main></div>;
}
