import { useQuery } from "@tanstack/react-query";
import {
  Bell,
  Check,
  Copy,
  Cpu,
  GitBranch,
  Layers,
  LayoutGrid,
  List,
  Lock,
  Plus,
  Radio,
  Server,
  Settings2,
  ShieldCheck,
  Sparkles,
  Terminal,
  Webhook,
} from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, messageFromError, type ProjectConfiguration } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { LoadingState, PageError } from "../../shared/ui";
import { configurationOrNull } from "./configuration";
import "./configuration.css";

export function ConfigurationPage() {
  const project = useCurrentProject();
  const navigate = useNavigate();
  const [viewMode, setViewMode] = useState<"cards" | "table">("cards");

  const configuration = useQuery({
    queryKey: queryKeys.configuration(project.key),
    queryFn: async ({ signal }) => api.getConfiguration(project.key, signal).catch(configurationOrNull),
  });

  if (configuration.isPending) return <LoadingState label="Loading configuration" />;
  if (configuration.isError) return <PageError message={messageFromError(configuration.error)} onRetry={() => void configuration.refetch()} />;

  const edit = () => navigate(`/projects/${encodeURIComponent(project.key)}/configuration/edit`);

  if (!configuration.data) {
    return (
      <section className="settings-view">
        <div className="view-header">
          <div>
            <h1>Configuration</h1>
            <p className="view-header-subtitle">Set up baseline repositories, collection sources, and alert triggers for your project.</p>
          </div>
          <div className="config-header-actions">
            <button className="secondary-button" type="button" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/configuration/notifications`)}><Bell size={16} />Notifications</button>
            <button className="primary-button" type="button" onClick={edit}>
              <Plus size={16} />
              Configure project
            </button>
          </div>
        </div>
        <section className="empty-projects compact">
          <Settings2 size={24} />
          <h2>Configuration required</h2>
          <p>Events and incidents need an environment, Git baseline, collection source, and trigger.</p>
        </section>
      </section>
    );
  }

  const value = configuration.data;
  const sourceDetails = parseSourceConfig(value.source);
  const triggerDetails = parseTriggerConfig(value.trigger);

  return (
    <section className="settings-view">
      <div className="view-header">
        <div>
          <h1>Configuration</h1>
          <p className="view-header-subtitle">
            Operational runtime scope, Git baseline, telemetry collection pipeline, and AI remediation engine.
          </p>
        </div>
        <div className="config-header-actions">
          <button className="secondary-button" type="button" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/configuration/notifications`)}><Bell size={16} />Notifications</button>
          <div className="view-mode-toggle" role="group" aria-label="View layout">
            <button
              type="button"
              className={`toggle-option ${viewMode === "cards" ? "active" : ""}`}
              onClick={() => setViewMode("cards")}
              title="Card view"
            >
              <LayoutGrid size={14} />
              <span>Cards</span>
            </button>
            <button
              type="button"
              className={`toggle-option ${viewMode === "table" ? "active" : ""}`}
              onClick={() => setViewMode("table")}
              title="Table view"
            >
              <List size={14} />
              <span>Table</span>
            </button>
          </div>
          <button className="primary-button" type="button" onClick={edit}>
            <Settings2 size={16} />
            Edit configuration
          </button>
        </div>
      </div>

      <div className="config-page-container">
        {/* Top Operational Vitals Strip */}
        <div className="config-overview-strip">
          <div className="config-overview-item">
            <div className="config-overview-icon">
              <Layers size={18} />
            </div>
            <div className="config-overview-meta">
              <label>Environment</label>
              <strong>{value.environment.name}</strong>
              <small>{value.environment.service || "No service scope"}</small>
            </div>
          </div>

          <div className="config-overview-item">
            <div className="config-overview-icon">
              <GitBranch size={18} />
            </div>
            <div className="config-overview-meta">
              <label>Production Baseline</label>
              <strong>
                {value.repository.productionBranch} @ {value.repository.deployedCommit.slice(0, 7)}
              </strong>
              <small>{formatScmProvider(value.repository.scmProvider)}</small>
            </div>
          </div>

          <div className="config-overview-item">
            <div className="config-overview-icon">
              <Radio size={18} />
            </div>
            <div className="config-overview-meta">
              <label>Collection Source</label>
              <strong>{formatSourceKind(value.source.kind)}</strong>
              <small>{value.source.capabilities.length} capabilities active</small>
            </div>
          </div>

          <div className="config-overview-item">
            <div className="config-overview-icon">
              <Sparkles size={18} />
            </div>
            <div className="config-overview-meta">
              <label>Remediation AI</label>
              <strong>{value.llm ? value.llm.model : "Not configured"}</strong>
              <small>{value.llm ? `${formatLlmProvider(value.llm.provider)} ready` : "Setup required"}</small>
            </div>
          </div>

          {value.remediation && (
            <div className="config-overview-item">
              <div className="config-overview-icon">
                <ShieldCheck size={18} />
              </div>
              <div className="config-overview-meta">
                <label>Automatic Hotfix</label>
                <strong>{value.remediation.executionMode === "auto_hotfix" ? "Draft PR" : "Analysis only"}</strong>
                <small>
                  {value.remediation.validationProfile.enabled === false
                    ? "Repository CI"
                    : `${value.remediation.validationProfile.requiredCommands.length} validation command${value.remediation.validationProfile.requiredCommands.length === 1 ? "" : "s"}`}
                </small>
              </div>
            </div>
          )}
        </div>

        {viewMode === "cards" ? (
          <div className="config-cards-grid">
            {/* 1. Environment & Service Card */}
            <div className="config-card">
              <div className="config-card-header">
                <div className="config-card-title-group">
                  <div className="config-card-icon">
                    <Server size={18} />
                  </div>
                  <h2>Environment & Scope</h2>
                </div>
                <span className="config-badge config-badge-success">
                  <span className="config-dot-pulse" />
                  Active
                </span>
              </div>
              <div className="config-card-body">
                <div className="config-prop-row">
                  <span className="config-prop-label">Environment</span>
                  <div className="config-prop-value">
                    <span className="config-provider-pill">{value.environment.name}</span>
                  </div>
                </div>
                <div className="config-prop-row">
                  <span className="config-prop-label">Target Service</span>
                  <div className="config-prop-value">
                    <code>{value.environment.service || "No service specified"}</code>
                  </div>
                </div>
                <div className="config-prop-row">
                  <span className="config-prop-label">Monitoring Scope</span>
                  <div className="config-prop-value">
                    <span className="config-badge config-badge-teal">Project scoped</span>
                  </div>
                </div>
              </div>
            </div>

            {/* 2. AI Remediation (LLM) Card */}
            <div className="config-card">
              <div className="config-card-header">
                <div className="config-card-title-group">
                  <div className="config-card-icon">
                    <Cpu size={18} />
                  </div>
                  <h2>Remediation Engine (LLM)</h2>
                </div>
                {value.llm ? (
                  <span className="config-badge config-badge-success">
                    <span className="config-dot-pulse" />
                    Ready
                  </span>
                ) : (
                  <span className="config-badge config-badge-amber">Setup needed</span>
                )}
              </div>
              <div className="config-card-body">
                {value.llm ? (
                  <>
                    <div className="config-prop-row">
                      <span className="config-prop-label">Provider</span>
                      <div className="config-prop-value">
                        <span className="config-provider-pill">{formatLlmProvider(value.llm.provider)}</span>
                      </div>
                    </div>
                    <div className="config-prop-row">
                      <span className="config-prop-label">Model</span>
                      <div className="config-prop-value">
                        <span className="config-branch-pill">
                          <code>{value.llm.model}</code>
                        </span>
                      </div>
                    </div>
                    <div className="config-prop-row">
                      <span className="config-prop-label">Base URL</span>
                      <div className="config-prop-value">
                        <code>{value.llm.baseUrl}</code>
                      </div>
                    </div>
                    <div className="config-prop-row">
                      <span className="config-prop-label">API Credential</span>
                      <div className="config-prop-value">
                        <span className="config-badge config-badge-teal">
                          <Lock size={12} />
                          Credential linked
                        </span>
                      </div>
                    </div>
                  </>
                ) : (
                  <p className="config-webhook-help">
                    Base URL and model are required to run automated remediation. Click &quot;Edit configuration&quot; to configure your LLM provider.
                  </p>
                )}
              </div>
            </div>

            {/* 3. Git Repository & Baseline Card */}
            <div className="config-card">
              <div className="config-card-header">
                <div className="config-card-title-group">
                  <div className="config-card-icon">
                    <GitBranch size={18} />
                  </div>
                  <h2>Git Repository & Baseline</h2>
                </div>
                {value.repository.credentialSecretId ? (
                  <span className="config-badge config-badge-teal">
                    <Lock size={12} />
                    Credential linked
                  </span>
                ) : (
                  <span className="config-badge config-badge-neutral">No credential</span>
                )}
              </div>
              <div className="config-card-body">
                <div className="config-prop-row">
                  <span className="config-prop-label">SCM Provider</span>
                  <div className="config-prop-value">
                    <span className="config-provider-pill">{formatScmProvider(value.repository.scmProvider)}</span>
                    <span className="config-code-pill">{value.repository.transport.toUpperCase()}</span>
                  </div>
                </div>
                <div className="config-prop-row">
                  <span className="config-prop-label">Production Baseline</span>
                  <div className="config-prop-value">
                    <div className="config-commit-group">
                      <span className="config-branch-pill">
                        <GitBranch size={13} />
                        {value.repository.productionBranch}
                      </span>
                      <span className="config-commit-chip">
                        <code>{value.repository.deployedCommit.slice(0, 7)}</code>
                      </span>
                      <CopyButton text={value.repository.deployedCommit} label="Copy commit" />
                    </div>
                  </div>
                </div>
                <div className="config-prop-row">
                  <span className="config-prop-label">Remote URL</span>
                  <div className="config-prop-value">
                    <div className="config-code-bar">
                      <code>{value.repository.remoteUrl}</code>
                      <CopyButton text={value.repository.remoteUrl} />
                    </div>
                  </div>
                </div>
              </div>
            </div>

            {/* 4. Collection Source Card */}
            <div className="config-card">
              <div className="config-card-header">
                <div className="config-card-title-group">
                  <div className="config-card-icon">
                    <Terminal size={18} />
                  </div>
                  <h2>Collection Source</h2>
                </div>
                {value.source.enabled ? (
                  <span className="config-badge config-badge-success">
                    <span className="config-dot-pulse" />
                    Enabled
                  </span>
                ) : (
                  <span className="config-badge config-badge-neutral">Disabled</span>
                )}
              </div>
              <div className="config-card-body">
                <div className="config-prop-row">
                  <span className="config-prop-label">Source Type</span>
                  <div className="config-prop-value">
                    <span className="config-provider-pill">{formatSourceKind(value.source.kind)}</span>
                  </div>
                </div>

                {sourceDetails.host && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Host Endpoint</span>
                    <div className="config-prop-value">
                      <code>
                        {sourceDetails.host}
                        {sourceDetails.port ? `:${sourceDetails.port}` : ""}
                      </code>
                      {sourceDetails.deploymentKind && (
                        <span className="config-code-pill">
                          {sourceDetails.deploymentKind === "docker"
                            ? `Docker${sourceDetails.containerName ? ` (${sourceDetails.containerName})` : ""}`
                            : "Host process"}
                        </span>
                      )}
                    </div>
                  </div>
                )}

                {sourceDetails.logPath && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Log Path</span>
                    <div className="config-prop-value">
                      <code>{sourceDetails.logPath}</code>
                      {sourceDetails.mode && <span className="config-code-pill">{sourceDetails.mode}</span>}
                    </div>
                  </div>
                )}

                {sourceDetails.endpoint && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Endpoint</span>
                    <div className="config-prop-value">
                      <div className="config-code-bar">
                        <code>{sourceDetails.endpoint}</code>
                        <CopyButton text={sourceDetails.endpoint} />
                      </div>
                    </div>
                  </div>
                )}

                {sourceDetails.resource && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Target Resource</span>
                    <div className="config-prop-value">
                      <code>
                        {sourceDetails.provider ? `${sourceDetails.provider} · ` : ""}
                        {sourceDetails.resource}
                      </code>
                    </div>
                  </div>
                )}

                <div className="config-prop-row">
                  <span className="config-prop-label">Capabilities</span>
                  <div className="config-prop-value">
                    <div className="config-capabilities-list">
                      {value.source.capabilities.map((cap) => (
                        <span key={cap} className="config-cap-tag">
                          <Check size={11} />
                          {cap}
                        </span>
                      ))}
                    </div>
                  </div>
                </div>
              </div>
            </div>

            {/* 5. Automatic Hotfix Card */}
            {value.remediation && (
              <div className="config-card">
                <div className="config-card-header">
                  <div className="config-card-title-group">
                    <div className="config-card-icon">
                      <ShieldCheck size={18} />
                    </div>
                    <h2>Automatic Hotfix</h2>
                  </div>
                  {value.remediation.executionMode === "auto_hotfix" ? (
                    <span className="config-badge config-badge-teal">
                      <span className="config-dot-pulse" />
                      Draft PR
                    </span>
                  ) : (
                    <span className="config-badge config-badge-neutral">Analysis only</span>
                  )}
                </div>
                <div className="config-card-body">
                  <div className="config-prop-row">
                    <span className="config-prop-label">Execution Mode</span>
                    <div className="config-prop-value">
                      <span className="config-provider-pill">
                        {value.remediation.executionMode === "auto_hotfix" ? "Draft PR" : "Analysis only"}
                      </span>
                      <span className="config-code-pill">
                        {value.remediation.agentLoopMode === "resilient_v1" ? "Resilient v1" : "Legacy"}
                      </span>
                    </div>
                  </div>

                  <div className="config-prop-row">
                    <span className="config-prop-label">Pre-validation</span>
                    <div className="config-prop-value">
                      {value.remediation.validationProfile.enabled === false ? (
                        <span className="config-provider-pill">Repository CI</span>
                      ) : (
                        <>
                          <span className="config-provider-pill">Local runner</span>
                          {value.remediation.validationProfile.imageDigest && (
                            <span className="config-code-pill" title={value.remediation.validationProfile.imageDigest}>
                              <code>{value.remediation.validationProfile.imageDigest.slice(0, 19)}...</code>
                            </span>
                          )}
                          <span className="config-code-pill">
                            {value.remediation.validationProfile.requiredCommands.length} command{value.remediation.validationProfile.requiredCommands.length === 1 ? "" : "s"}
                          </span>
                        </>
                      )}
                    </div>
                  </div>

                  <div className="config-prop-row">
                    <span className="config-prop-label">Publication Target</span>
                    <div className="config-prop-value">
                      <span className="config-branch-pill">
                        <GitBranch size={13} />
                        {value.remediation.publication.branchPrefix}/*
                      </span>
                      {value.remediation.publication.gitCredentialSecretId ? (
                        <span className="config-badge config-badge-teal">
                          <Lock size={12} />
                          Git write linked
                        </span>
                      ) : (
                        <span className="config-badge config-badge-neutral">No write credential</span>
                      )}
                    </div>
                  </div>

                  <div className="config-prop-row">
                    <span className="config-prop-label">Change Limits</span>
                    <div className="config-prop-value">
                      <span className="config-code-pill">
                        Max {value.remediation.changePolicy.maxChangedFiles} files
                      </span>
                      <span className="config-code-pill">
                        Max {value.remediation.changePolicy.maxChangedLines} lines
                      </span>
                      {value.remediation.changePolicy.allowedPaths.length > 0 && (
                        <span className="config-code-pill" title={`Allowed: ${value.remediation.changePolicy.allowedPaths.join(", ")}`}>
                          {value.remediation.changePolicy.allowedPaths.join(", ")}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* 6. Trigger & Webhook Card */}
            <div className="config-card">
              <div className="config-card-header">
                <div className="config-card-title-group">
                  <div className="config-card-icon">
                    <Webhook size={18} />
                  </div>
                  <h2>Incident Trigger & Inbound Webhook</h2>
                </div>
                {value.trigger.enabled ? (
                  <span className="config-badge config-badge-success">
                    <span className="config-dot-pulse" />
                    Enabled
                  </span>
                ) : (
                  <span className="config-badge config-badge-neutral">Disabled</span>
                )}
              </div>
              <div className="config-card-body">
                <div className="config-prop-row">
                  <span className="config-prop-label">Trigger Mode</span>
                  <div className="config-prop-value">
                    <span className="config-provider-pill">{formatTriggerKind(value.trigger.kind)}</span>
                    <span className="config-provider-pill">{formatTriggerProvider(value.trigger.config as Record<string, unknown>)}</span>
                  </div>
                </div>

                {triggerDetails.topicArn && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Topic ARN</span>
                    <div className="config-prop-value">
                      <code>{triggerDetails.topicArn}</code>
                    </div>
                  </div>
                )}

                {triggerDetails.matchExpression && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Filter Match</span>
                    <div className="config-prop-value">
                      <span className="config-code-pill">{triggerDetails.matchExpression}</span>
                    </div>
                  </div>
                )}

                {triggerDetails.groupingWindowSeconds && (
                  <div className="config-prop-row">
                    <span className="config-prop-label">Grouping Window</span>
                    <div className="config-prop-value">
                      <span>{Math.round(triggerDetails.groupingWindowSeconds / 60)} minutes ({triggerDetails.groupingWindowSeconds}s)</span>
                    </div>
                  </div>
                )}

                {value.trigger.kind === "signed_webhook" && value.trigger.inboundUrl && (
                  <WebhookUrlBox url={value.trigger.inboundUrl} />
                )}
              </div>
            </div>
          </div>
        ) : (
          /* Table View */
          <div className="config-table-card">
            <table>
              <thead>
                <tr>
                  <th>Component</th>
                  <th>Type / Provider</th>
                  <th>Configuration</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>
                    <div className="config-component-cell">
                      <div className="config-component-icon">
                        <Server size={15} />
                      </div>
                      <span>Environment</span>
                    </div>
                  </td>
                  <td>
                    <span className="config-provider-pill">{value.environment.name}</span>
                  </td>
                  <td>
                    <code>{value.environment.service || "No service"}</code>
                  </td>
                  <td>
                    <span className="config-badge config-badge-success">
                      <span className="config-dot-pulse" />
                      Active
                    </span>
                  </td>
                </tr>

                <tr>
                  <td>
                    <div className="config-component-cell">
                      <div className="config-component-icon">
                        <GitBranch size={15} />
                      </div>
                      <span>Git repository</span>
                    </div>
                  </td>
                  <td>
                    <span className="config-provider-pill">{formatScmProvider(value.repository.scmProvider)}</span>
                  </td>
                  <td>
                    <div className="config-code-bar">
                      <code>{value.repository.remoteUrl}</code>
                      <CopyButton text={value.repository.remoteUrl} />
                    </div>
                  </td>
                  <td>
                    {value.repository.credentialSecretId ? (
                      <span className="config-badge config-badge-teal">
                        <Lock size={12} />
                        Credential linked
                      </span>
                    ) : (
                      <span className="config-badge config-badge-neutral">No credential</span>
                    )}
                  </td>
                </tr>

                <tr>
                  <td>
                    <div className="config-component-cell">
                      <div className="config-component-icon">
                        <Terminal size={15} />
                      </div>
                      <span>Collection source</span>
                    </div>
                  </td>
                  <td>
                    <span className="config-provider-pill">{formatSourceKind(value.source.kind)}</span>
                  </td>
                  <td>
                    <span>{sourceConfigurationSummary(value.source.config)}</span>
                  </td>
                  <td>
                    {value.source.enabled ? (
                      <span className="config-badge config-badge-success">
                        <span className="config-dot-pulse" />
                        Enabled
                      </span>
                    ) : (
                      <span className="config-badge config-badge-neutral">Disabled</span>
                    )}
                  </td>
                </tr>

                <tr>
                  <td>
                    <div className="config-component-cell">
                      <div className="config-component-icon">
                        <Webhook size={15} />
                      </div>
                      <span>Trigger</span>
                    </div>
                  </td>
                  <td>
                    <span className="config-provider-pill">{formatTriggerKind(value.trigger.kind)}</span>
                  </td>
                  <td>
                    <span>{triggerConfigurationSummary(value.trigger.config)}</span>
                  </td>
                  <td>
                    {value.trigger.enabled ? (
                      <span className="config-badge config-badge-success">
                        <span className="config-dot-pulse" />
                        Enabled
                      </span>
                    ) : (
                      <span className="config-badge config-badge-neutral">Disabled</span>
                    )}
                  </td>
                </tr>

                {value.trigger.kind === "signed_webhook" && value.trigger.inboundUrl && (
                  <tr>
                    <td>
                      <div className="config-component-cell">
                        <div className="config-component-icon">
                          <ShieldCheck size={15} />
                        </div>
                        <span>Inbound webhook</span>
                      </div>
                    </td>
                    <td>
                      <span className="config-badge config-badge-amber">Public URL</span>
                    </td>
                    <td>
                      <div className="config-code-bar">
                        <code>{value.trigger.inboundUrl}</code>
                        <CopyButton text={value.trigger.inboundUrl} label="Copy" />
                      </div>
                    </td>
                    <td>
                      <span className="config-badge config-badge-amber">
                        <ShieldCheck size={12} />
                        Admin only
                      </span>
                    </td>
                  </tr>
                )}

                <tr>
                  <td>
                    <div className="config-component-cell">
                      <div className="config-component-icon">
                        <Cpu size={15} />
                      </div>
                      <span>LLM provider</span>
                    </div>
                  </td>
                  <td>
                    <span className="config-provider-pill">{value.llm ? formatLlmProvider(value.llm.provider) : "Not configured"}</span>
                  </td>
                  <td>
                    {value.llm ? (
                      <span className="config-commit-chip">
                        <code>{value.llm.model}</code>
                      </span>
                    ) : (
                      "Base URL and model are required to run remediation"
                    )}
                  </td>
                  <td>
                    {value.llm?.credentialSecretId ? (
                      <span className="config-badge config-badge-teal">
                        <Lock size={12} />
                        Credential linked
                      </span>
                    ) : (
                      <span className="config-badge config-badge-neutral">No credential</span>
                    )}
                  </td>
                </tr>

                {value.remediation && (
                  <tr>
                    <td>
                      <div className="config-component-cell">
                        <div className="config-component-icon">
                          <ShieldCheck size={15} />
                        </div>
                        <span>Automatic hotfix</span>
                      </div>
                    </td>
                    <td>
                      <span className="config-provider-pill">
                        {value.remediation.executionMode === "auto_hotfix" ? "Draft PR" : "Analysis only"}
                      </span>
                    </td>
                    <td>
                      <span>
                        {value.remediation.validationProfile.enabled === false
                          ? "Repository CI"
                          : `${value.remediation.validationProfile.requiredCommands.length} local command${value.remediation.validationProfile.requiredCommands.length === 1 ? "" : "s"}`}
                        {" · "}
                        {value.remediation.changePolicy.maxChangedFiles} files / {value.remediation.changePolicy.maxChangedLines} lines max
                      </span>
                    </td>
                    <td>
                      {value.remediation.executionMode === "auto_hotfix" ? (
                        <span className="config-badge config-badge-teal">
                          <span className="config-dot-pulse" />
                          Draft PR
                        </span>
                      ) : (
                        <span className="config-badge config-badge-neutral">Analysis only</span>
                      )}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

function parseSourceConfig(source: ProjectConfiguration["source"]) {
  const config = (source.config || {}) as Record<string, unknown>;
  const deployment =
    config.deployment && typeof config.deployment === "object" && !Array.isArray(config.deployment)
      ? (config.deployment as Record<string, unknown>)
      : undefined;

  return {
    host: typeof config.host === "string" ? config.host : undefined,
    port: typeof config.port === "number" ? config.port : undefined,
    user: typeof config.user === "string" ? config.user : undefined,
    logPath: typeof config.logPath === "string" ? config.logPath : undefined,
    projectFolder: typeof config.projectFolder === "string" ? config.projectFolder : undefined,
    mode: typeof config.mode === "string" ? config.mode : undefined,
    deploymentKind: typeof deployment?.kind === "string" ? deployment.kind : undefined,
    containerName: typeof deployment?.containerName === "string" ? deployment.containerName : undefined,
    provider: typeof config.provider === "string" ? config.provider : undefined,
    region: typeof config.region === "string" ? config.region : undefined,
    resource: typeof config.resource === "string" ? config.resource : undefined,
    endpoint: typeof config.endpoint === "string" ? config.endpoint : undefined,
    transport: typeof config.transport === "string" ? config.transport : undefined,
  };
}

function parseTriggerConfig(trigger: ProjectConfiguration["trigger"]) {
  const config = (trigger.config || {}) as Record<string, unknown>;
  const aws =
    config.awsCloudWatch && typeof config.awsCloudWatch === "object" && !Array.isArray(config.awsCloudWatch)
      ? (config.awsCloudWatch as Record<string, unknown>)
      : undefined;

  return {
    provider: typeof config.provider === "string" ? config.provider : undefined,
    topicArn: typeof aws?.topicArn === "string" ? aws.topicArn : undefined,
    matchExpression: typeof config.matchExpression === "string" ? config.matchExpression : undefined,
    eventTypes: Array.isArray(config.eventTypes) ? config.eventTypes.filter((v): v is string => typeof v === "string") : [],
    deduplicationKey: typeof config.deduplicationKey === "string" ? config.deduplicationKey : undefined,
    groupingWindowSeconds: typeof config.groupingWindowSeconds === "number" ? config.groupingWindowSeconds : undefined,
  };
}

function formatScmProvider(provider: string): string {
  switch (provider) {
    case "yunxiao":
      return "Aliyun Codeup (Yunxiao)";
    case "github":
      return "GitHub";
    case "gitlab":
      return "GitLab";
    case "gitee":
      return "Gitee";
    case "generic":
      return "Generic Git";
    default:
      return provider;
  }
}

function formatSourceKind(kind: string): string {
  switch (kind) {
    case "ssh":
      return "SSH Collector";
    case "cloud":
      return "Cloud Telemetry";
    case "mcp":
      return "MCP Server";
    case "kubernetes":
      return "Kubernetes Pod Logs";
    default:
      return kind.toUpperCase();
  }
}

function formatTriggerKind(kind: string): string {
  switch (kind) {
    case "signed_webhook":
      return "Signed Webhook";
    case "custom_rule":
      return "Custom Rule";
    default:
      return kind.replace(/_/g, " ");
  }
}

function formatTriggerProvider(config: Record<string, unknown> | undefined): string {
  if (!config) return "Generic Webhook";
  if (config.provider === "tencent_cls") return "Tencent Cloud CLS";
  if (config.provider === "aws_cloudwatch") return "AWS CloudWatch";
  if (config.provider === "generic") return "Generic Webhook";
  return typeof config.provider === "string" ? config.provider : "Webhook";
}

function formatLlmProvider(provider?: string): string {
  if (!provider) return "Not configured";
  if (provider === "openai") return "OpenAI";
  return provider.charAt(0).toUpperCase() + provider.slice(1);
}

function sourceConfigurationSummary(config: ProjectConfiguration["source"]["config"]): string {
  if (typeof config.provider === "string" && typeof config.resource === "string") return `${config.provider} · ${config.resource}`;
  if (typeof config.endpoint === "string") return config.endpoint;
  if (typeof config.host === "string" && typeof config.logPath === "string") {
    const deployment =
      typeof config.deployment === "object" && config.deployment !== null && !Array.isArray(config.deployment)
        ? (config.deployment as Record<string, unknown>)
        : {};
    const kind = deployment.kind === "docker" ? "Docker" : "Host process";
    return `${config.host}${config.logPath} · ${kind}`;
  }
  return "Configured";
}

function triggerConfigurationSummary(config: ProjectConfiguration["trigger"]["config"]): string {
  if (typeof config.matchExpression === "string") return config.matchExpression;
  if (config.provider === "aws_cloudwatch") {
    const aws =
      typeof config.awsCloudWatch === "object" && config.awsCloudWatch !== null && !Array.isArray(config.awsCloudWatch)
        ? (config.awsCloudWatch as Record<string, unknown>)
        : {};
    return typeof aws.topicArn === "string" ? `AWS CloudWatch · ${aws.topicArn}` : "AWS CloudWatch";
  }
  if (config.provider === "tencent_cls") return "Tencent Cloud CLS";
  if (config.provider === "generic") return "Generic webhook";
  if (Array.isArray(config.eventTypes)) return config.eventTypes.filter((value): value is string => typeof value === "string").join(", ") || "Signed webhook";
  return "Configured";
}

function CopyButton({ text, label }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // ignore
    }
  };

  return (
    <button
      type="button"
      className={`config-inline-copy ${copied ? "copied" : ""}`}
      onClick={(e) => {
        e.stopPropagation();
        void copy();
      }}
      title={copied ? "Copied!" : `Copy ${label || "value"}`}
    >
      {copied ? <Check size={12} /> : <Copy size={12} />}
      {label && <span>{copied ? "Copied" : label}</span>}
    </button>
  );
}

function WebhookUrlBox({ url }: { url: string }) {
  const [copied, setCopied] = useState(false);

  const copyUrl = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      // ignore
    }
  };

  return (
    <div className="config-webhook-banner">
      <div className="config-webhook-banner-header">
        <span className="config-webhook-label">
          <Webhook size={14} />
          Inbound Webhook Ingestion Endpoint
        </span>
        <span className="config-badge config-badge-amber">
          <ShieldCheck size={12} />
          Admin Only · HMAC Verified
        </span>
      </div>
      <div className="config-webhook-input-group">
        <code className="config-webhook-code">{url}</code>
        <button
          type="button"
          className={`config-webhook-copy-btn ${copied ? "copied" : ""}`}
          onClick={() => void copyUrl()}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />}
          <span>{copied ? "Copied" : "Copy URL"}</span>
        </button>
      </div>
      <p className="config-webhook-help">
        Configure your alert provider (Tencent Cloud CLS Alarm, AWS CloudWatch SNS, or Alertmanager) to POST incident payloads to this secure URL.
      </p>
    </div>
  );
}
