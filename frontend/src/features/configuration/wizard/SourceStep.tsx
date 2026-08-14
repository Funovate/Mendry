import { FileSearch } from "lucide-react";
import type { ProjectSecret, SourceKind } from "../../../api";
import { CredentialField } from "./CredentialField";

const SOURCE_CREDENTIAL_KINDS: Record<SourceKind, { value: ProjectSecret["kind"]; label: string }[]> = {
  ssh: [
    { value: "ssh_private_key", label: "SSH private key" },
    { value: "ssh_password", label: "SSH password" },
  ],
  mcp: [
    { value: "http_bearer", label: "HTTP bearer token" },
    { value: "http_header", label: "HTTP header" },
  ],
  cloud: [
    { value: "http_bearer", label: "HTTP bearer token" },
    { value: "http_header", label: "HTTP header" },
  ],
};

export function SourceStep({
  sourceName, setSourceName, sourceKind, onSourceKindChange, sourceCredentialId, setSourceCredentialId,
  sourceCapabilities, toggleCapability, sourceEndpoint, setSourceEndpoint, mcpTransport, setMcpTransport,
  mcpHeaders, setMcpHeaders, evidenceProfile, setEvidenceProfile, queryScope, setQueryScope,
  sourceHost, setSourceHost, sshPort, setSshPort, sshUser, setSshUser, projectFolder, setProjectFolder,
  logPath, setLogPath, readMode, setReadMode, cloudProvider, setCloudProvider, cloudRegion, setCloudRegion,
  sourceResource, setSourceResource, knownSecrets, createCredential, creatingCredential, createCredentialError,
  updateCredential, updatingCredential = false, updateCredentialError,
}: {
  sourceName: string;
  setSourceName: (value: string) => void;
  sourceKind: SourceKind;
  onSourceKindChange: (kind: SourceKind) => void;
  sourceCredentialId: string;
  setSourceCredentialId: (value: string) => void;
  sourceCapabilities: string[];
  toggleCapability: (capability: string) => void;
  sourceEndpoint: string;
  setSourceEndpoint: (value: string) => void;
  mcpTransport: string;
  setMcpTransport: (value: string) => void;
  mcpHeaders: string;
  setMcpHeaders: (value: string) => void;
  evidenceProfile: string;
  setEvidenceProfile: (value: string) => void;
  queryScope: string;
  setQueryScope: (value: string) => void;
  sourceHost: string;
  setSourceHost: (value: string) => void;
  sshPort: number;
  setSshPort: (value: number) => void;
  sshUser: string;
  setSshUser: (value: string) => void;
  projectFolder: string;
  setProjectFolder: (value: string) => void;
  logPath: string;
  setLogPath: (value: string) => void;
  readMode: string;
  setReadMode: (value: string) => void;
  cloudProvider: string;
  setCloudProvider: (value: string) => void;
  cloudRegion: string;
  setCloudRegion: (value: string) => void;
  sourceResource: string;
  setSourceResource: (value: string) => void;
  knownSecrets: ProjectSecret[];
  createCredential: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => Promise<ProjectSecret>;
  creatingCredential: boolean;
  createCredentialError?: unknown;
  updateCredential: (input: { secretId: string; name: string; value?: string }) => Promise<ProjectSecret>;
  updatingCredential?: boolean;
  updateCredentialError?: unknown;
}) {
  return <section>
    <div className="setup-card-title"><FileSearch size={20} /><div><h2>Collection source</h2><p>Configure the complete persisted contract for one read-only source.</p></div></div>
    <div className="source-form">
      <label>Source name<input aria-label="Source name" value={sourceName} onChange={(event) => setSourceName(event.target.value)} /></label>
      <label>Source type<select aria-label="Source type" value={sourceKind} onChange={(event) => onSourceKindChange(event.target.value as SourceKind)}>
        <option value="cloud">Cloud logs</option>
        <option value="mcp">MCP</option>
        <option value="ssh">SSH logs</option>
      </select></label>
    </div>
    <CredentialField
      label="Source credential reference" value={sourceCredentialId} onChange={setSourceCredentialId} secrets={knownSecrets}
      createLabelPrefix="Source" allowedCreateKinds={SOURCE_CREDENTIAL_KINDS[sourceKind]} onCreate={createCredential}
      creating={creatingCredential} createError={createCredentialError}
      onUpdate={updateCredential} updating={updatingCredential} updateError={updateCredentialError}
    />
    {sourceKind === "mcp" && <>
      <label>MCP endpoint<input aria-label="MCP endpoint" value={sourceEndpoint} onChange={(event) => setSourceEndpoint(event.target.value)} /></label>
      <div className="source-form">
        <label>MCP transport<select aria-label="MCP transport" value={mcpTransport} onChange={(event) => setMcpTransport(event.target.value)}>
          <option value="http">HTTP</option>
          <option value="streamable_http">Streamable HTTP</option>
          <option value="sse">SSE</option>
        </select></label>
        <label>Evidence profile<input aria-label="Evidence profile" value={evidenceProfile} onChange={(event) => setEvidenceProfile(event.target.value)} /></label>
      </div>
      <label>Query scope<textarea aria-label="Query scope" value={queryScope} onChange={(event) => setQueryScope(event.target.value)} /></label>
      <label>Non-secret headers JSON<textarea aria-label="MCP headers JSON" value={mcpHeaders} onChange={(event) => setMcpHeaders(event.target.value)} /></label>
    </>}
    {sourceKind === "ssh" && <>
      <div className="source-form">
        <label>SSH host<input aria-label="SSH host" value={sourceHost} onChange={(event) => setSourceHost(event.target.value)} /></label>
        <label>SSH port<input aria-label="SSH port" type="number" min="1" max="65535" value={sshPort} onChange={(event) => setSshPort(Number(event.target.value))} /></label>
      </div>
      <div className="source-form">
        <label>SSH user<input aria-label="SSH user" value={sshUser} onChange={(event) => setSshUser(event.target.value)} /></label>
        <label>Read mode<select aria-label="Read mode" value={readMode} onChange={(event) => setReadMode(event.target.value)}>
          <option value="tail">Tail</option>
          <option value="snapshot">Snapshot</option>
        </select></label>
      </div>
      <label>Project folder<input aria-label="Project folder" value={projectFolder} onChange={(event) => setProjectFolder(event.target.value)} /></label>
      <label>Log path<input aria-label="Log path" value={logPath} onChange={(event) => setLogPath(event.target.value)} /></label>
    </>}
    {sourceKind === "cloud" && <>
      <div className="source-form">
        <label>Cloud provider<input aria-label="Cloud provider" value={cloudProvider} onChange={(event) => setCloudProvider(event.target.value)} /></label>
        <label>Cloud region<input aria-label="Cloud region" value={cloudRegion} onChange={(event) => setCloudRegion(event.target.value)} /></label>
      </div>
      <label>Cloud resource<input aria-label="Cloud resource" value={sourceResource} onChange={(event) => setSourceResource(event.target.value)} /></label>
    </>}
    <fieldset className="capability-editor">
      <legend>Collection capabilities</legend>
      {["pull_collection", "context_collection", "metric_collection", "push_ingestion"].map((capability) => <label key={capability}>
        <input type="checkbox" checked={sourceCapabilities.includes(capability)} onChange={() => toggleCapability(capability)} />{capability.replaceAll("_", " ")}
      </label>)}
    </fieldset>
  </section>;
}
