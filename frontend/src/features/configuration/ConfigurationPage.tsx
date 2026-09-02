import { useQuery } from "@tanstack/react-query";
import { Check, CircleHelp, Copy, Eye, GitBranch, Plus, Settings2 } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, messageFromError, type ProjectConfiguration } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { LoadingState, PageError } from "../../shared/ui";
import { configurationOrNull } from "./configuration";

export function ConfigurationPage() {
  const project = useCurrentProject();
  const navigate = useNavigate();
  const configuration = useQuery({
    queryKey: queryKeys.configuration(project.key),
    queryFn: async ({ signal }) => api.getConfiguration(project.key, signal).catch(configurationOrNull),
  });
  if (configuration.isPending) return <LoadingState label="Loading configuration" />;
  if (configuration.isError) return <PageError message={messageFromError(configuration.error)} onRetry={() => void configuration.refetch()} />;
  const edit = () => navigate(`/projects/${encodeURIComponent(project.key)}/configuration/edit`);
  if (!configuration.data) {
    return <section className="settings-view">
      <div className="view-header">
        <div><div className="eyebrow">{project.name}</div><h1>Configuration</h1><p>This project exists, but its environment, repository, source, and trigger have not been configured.</p></div>
        {project.capabilities.manageConfiguration && <button className="primary-button" type="button" onClick={edit}><Plus size={16} />Configure project</button>}
      </div>
      <section className="empty-projects compact"><Settings2 size={24} /><h2>Configuration required</h2><p>Events and incidents need an environment, Git baseline, collection source, and trigger.</p></section>
    </section>;
  }

  const value = configuration.data;
  return <section className="settings-view">
    <div className="view-header">
      <div><div className="eyebrow">{project.name} / {value.environment.name}</div><h1>Configuration</h1><p>Persistent project collection and production context.</p></div>
      {project.capabilities.manageConfiguration ? <button className="primary-button" type="button" onClick={edit}><Settings2 size={16} />Edit configuration</button> : <span className="readonly-note">{project.role} access</span>}
    </div>
    <div className="setup-summary">
      <div><GitBranch size={17} /><span><strong>Production baseline</strong><small>{value.repository.productionBranch}@{value.repository.deployedCommit}</small></span></div>
      <div><Eye size={17} /><span><strong>Collection</strong><small>{value.source.kind} · {value.source.capabilities.join(", ")}</small></span></div>
    </div>
    <div className="table-wrap"><table><thead><tr><th>Component</th><th>Type / provider</th><th>Configuration</th><th>Status</th></tr></thead><tbody>
      <tr><td>Environment</td><td>{value.environment.name}</td><td>{value.environment.service || "No service"}</td><td>Active</td></tr>
      <tr><td>Git repository</td><td>{value.repository.scmProvider}</td><td><code>{value.repository.remoteUrl}</code></td><td>{value.repository.credentialSecretId ? "Credential linked" : "No credential"}</td></tr>
      <tr><td>Collection source</td><td>{value.source.kind}</td><td>{sourceConfigurationSummary(value.source.config)}</td><td>{value.source.enabled ? "Enabled" : "Disabled"}</td></tr>
      <tr><td>Trigger</td><td>{value.trigger.kind.replace("_", " ")}</td><td>{triggerConfigurationSummary(value.trigger.config)}</td><td>{value.trigger.enabled ? "Enabled" : "Disabled"}</td></tr>
      {value.trigger.kind === "signed_webhook" && value.trigger.inboundUrl && <tr>
        <td>Inbound webhook</td>
        <td>Public URL</td>
        <td><InboundUrlCopy value={value.trigger.inboundUrl} /></td>
        <td>Admin only</td>
      </tr>}
      <tr><td>LLM provider</td><td>{value.llm?.provider || "Not configured"}</td><td>{value.llm ? <code>{value.llm.model}</code> : "Base URL and model are required to run remediation"}</td><td>{value.llm?.credentialSecretId ? "Credential linked" : "No credential"}</td></tr>
    </tbody></table></div>
    <section className="metadata-note"><CircleHelp size={17} /><p>Credential values are write-only. This page receives only stable secret references and never plaintext, ciphertext, or nonce values.</p></section>
  </section>;
}

function sourceConfigurationSummary(config: ProjectConfiguration["source"]["config"]): string {
  if (typeof config.provider === "string" && typeof config.resource === "string") return `${config.provider} · ${config.resource}`;
  if (typeof config.endpoint === "string") return config.endpoint;
  if (typeof config.host === "string" && typeof config.logPath === "string") {
    const deployment = typeof config.deployment === "object" && config.deployment !== null && !Array.isArray(config.deployment)
      ? config.deployment as Record<string, unknown>
      : {};
    const kind = deployment.kind === "docker" ? "Docker" : "Host process";
    return `${config.host}${config.logPath} · ${kind}`;
  }
  return "Configured";
}

function triggerConfigurationSummary(config: ProjectConfiguration["trigger"]["config"]): string {
  if (typeof config.matchExpression === "string") return config.matchExpression;
  if (config.provider === "tencent_cls") return "Tencent Cloud CLS";
  if (config.provider === "generic") return "Generic webhook";
  if (Array.isArray(config.eventTypes)) return config.eventTypes.filter((value): value is string => typeof value === "string").join(", ") || "Signed webhook";
  return "Configured";
}

function InboundUrlCopy({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copyUrl = async () => {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };
  return <div className="inbound-url-copy">
    <code>{value}</code>
    <button className="secondary-button" type="button" onClick={() => void copyUrl()}>
      {copied ? <Check size={16} /> : <Copy size={16} />}{copied ? "Copied" : "Copy URL"}
    </button>
  </div>;
}
