import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, LoaderCircle } from "lucide-react";
import { useState } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { api, messageFromError, type ListResult, type ProjectConfiguration, type ProjectSecret, type RepositoryRefs, type SourceKind, type TriggerKind } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { ErrorNotice, LoadingState, PageError } from "../../shared/ui";
import {
  buildSourceConfig, buildTriggerConfig, configurationOrNull, defaultSourceCapabilities,
  readConfigNumber, readConfigString, readStringRecord,
} from "./configuration";
import { LLMStep } from "./wizard/LLMStep";
import { RepositoryStep } from "./wizard/RepositoryStep";
import { ReviewStep } from "./wizard/ReviewStep";
import { SourceStep } from "./wizard/SourceStep";
import { TriggerStep } from "./wizard/TriggerStep";

type StepId = "repository" | "source" | "trigger" | "llm" | "review";

const STEPS: { id: StepId; label: string }[] = [
  { id: "repository", label: "Git repository" },
  { id: "source", label: "Collection source" },
  { id: "trigger", label: "Trigger" },
  { id: "llm", label: "LLM provider" },
  { id: "review", label: "Review" },
];

export function ConfigurationEditorPage() {
  const project = useCurrentProject();
  const navigate = useNavigate();
  const current = useQuery({
    queryKey: queryKeys.configuration(project.key),
    queryFn: async ({ signal }) => api.getConfiguration(project.key, signal).catch(configurationOrNull),
  });
  const secrets = useQuery({
    queryKey: queryKeys.secrets(project.key),
    queryFn: ({ signal }) => api.listSecrets(project.key, signal),
    enabled: project.capabilities.manageConfiguration,
  });
  if (!project.capabilities.manageConfiguration) return <Navigate to={`/projects/${encodeURIComponent(project.key)}/configuration`} replace />;
  if (current.isPending || secrets.isPending) return <LoadingState label="Loading configuration editor" />;
  if (current.isError) return <PageError message={messageFromError(current.error)} onRetry={() => void current.refetch()} />;
  if (secrets.isError) return <PageError message={messageFromError(secrets.error)} onRetry={() => void secrets.refetch()} />;
  return <ConfigurationWizard current={current.data} secrets={secrets.data.items} onCancel={() => navigate(`/projects/${encodeURIComponent(project.key)}/configuration`)} />;
}

function ConfigurationWizard({ current, secrets }: { current: ProjectConfiguration | null; secrets: ProjectSecret[]; onCancel: () => void }) {
  const project = useCurrentProject();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [activeStep, setActiveStep] = useState<StepId>("repository");
  const [environmentKey] = useState(current?.environment.key ?? project.key);
  const [environmentName] = useState(current?.environment.name ?? project.name);
  const [service] = useState(current?.environment.service ?? "");
  const [remoteUrl, setRemoteUrl] = useState(current?.repository.remoteUrl ?? "https://git.example.internal/platform/service.git");
  const [scmProvider, setScmProvider] = useState<ProjectConfiguration["repository"]["scmProvider"]>(current?.repository.scmProvider ?? "generic");
  const [transport, setTransport] = useState<ProjectConfiguration["repository"]["transport"]>(current?.repository.transport ?? "https");
  const [repositorySecretId, setRepositorySecretId] = useState(current?.repository.credentialSecretId ?? "");
  const [productionBranch, setProductionBranch] = useState(current?.repository.productionBranch ?? "");
  const [deployedCommit, setDeployedCommit] = useState(current?.repository.deployedCommit ?? "");
  const [repositoryBranches, setRepositoryBranches] = useState<{ name: string; commit: string }[]>(
    current?.repository.productionBranch && current.repository.deployedCommit
      ? [{ name: current.repository.productionBranch, commit: current.repository.deployedCommit }]
      : [],
  );
  const [baselineReady, setBaselineReady] = useState(Boolean(current?.repository.productionBranch && current.repository.deployedCommit));
  const [sourceName, setSourceName] = useState(current?.source.name ?? "production-logs");
  const [sourceKind, setSourceKind] = useState<SourceKind>(current?.source.kind ?? "cloud");
  const [sourceCredentialId, setSourceCredentialId] = useState(current?.source.credentialSecretId ?? "");
  const [sourceCapabilities, setSourceCapabilities] = useState(current?.source.capabilities ?? defaultSourceCapabilities(current?.source.kind ?? "cloud"));
  const [sourceEndpoint, setSourceEndpoint] = useState(readConfigString(current?.source.config, "endpoint", "https://mcp.example.internal/mcp"));
  const [mcpTransport, setMcpTransport] = useState(readConfigString(current?.source.config, "transport", "streamable_http"));
  const [mcpHeaders, setMcpHeaders] = useState(JSON.stringify(readStringRecord(current?.source.config, "headers"), null, 2));
  const [evidenceProfile, setEvidenceProfile] = useState(readConfigString(current?.source.config, "evidenceProfile", "errors-context"));
  const [queryScope, setQueryScope] = useState(readConfigString(current?.source.config, "queryScope", "project"));
  const [sourceHost, setSourceHost] = useState(readConfigString(current?.source.config, "host", "api-prod.internal"));
  const [sshPort, setSshPort] = useState(readConfigNumber(current?.source.config, "port", 22));
  const [sshUser, setSshUser] = useState(readConfigString(current?.source.config, "user", "collector"));
  const [projectFolder, setProjectFolder] = useState(readConfigString(current?.source.config, "projectFolder", "/srv/app"));
  const [logPath, setLogPath] = useState(readConfigString(current?.source.config, "logPath", "/var/log/app.log"));
  const [readMode, setReadMode] = useState(readConfigString(current?.source.config, "mode", "tail"));
  const [cloudProvider, setCloudProvider] = useState(readConfigString(current?.source.config, "provider", "generic"));
  const [cloudRegion, setCloudRegion] = useState(readConfigString(current?.source.config, "region", "default"));
  const [sourceResource, setSourceResource] = useState(readConfigString(current?.source.config, "resource", "service-logs"));
  const [triggerName, setTriggerName] = useState(current?.trigger.name ?? "incident-rule");
  const [triggerKind, setTriggerKind] = useState<TriggerKind>(current?.trigger.kind ?? "custom_rule");
  const [inboundUrl, setInboundUrl] = useState(current?.trigger.inboundUrl ?? "");
  const [groupingWindowSeconds, setGroupingWindowSeconds] = useState(readConfigNumber(current?.trigger.config, "groupingWindowSeconds", 900));
  const [matchExpression, setMatchExpression] = useState(readConfigString(current?.trigger.config, "matchExpression", "level=ERROR"));
  const [llmBaseUrl, setLlmBaseUrl] = useState(current?.llm?.baseUrl ?? "https://api.openai.com");
  const [llmCredentialId, setLlmCredentialId] = useState(current?.llm?.credentialSecretId ?? "");
  const [llmModel, setLlmModel] = useState(current?.llm?.model ?? "");
  const [llmModels, setLlmModels] = useState<string[]>(current?.llm?.model ? [current.llm.model] : []);
  const [llmChatReady, setLlmChatReady] = useState(false);
  const [validationError, setValidationError] = useState("");

  const createSecret = useMutation({
    mutationFn: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => api.createSecret(project.key, input),
    onSuccess: (created) => {
      queryClient.setQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key), (current) => ({
        items: [...(current?.items ?? []), created],
        total: (current?.total ?? 0) + 1,
      }));
    },
  });
  const updateSecret = useMutation({
    mutationFn: (input: { secretId: string; name: string; value?: string }) => api.updateSecret(project.key, input.secretId, { name: input.name, value: input.value }),
    onSuccess: (updated) => {
      queryClient.setQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key), (current) => current ? {
        items: current.items.map((item) => item.id === updated.id ? updated : item),
        total: current.total,
      } : current);
    },
  });
  const applyRepositoryRefs = (refs: RepositoryRefs) => {
    setRepositoryBranches(refs.branches);
    setProductionBranch(refs.defaultBranch);
    setDeployedCommit(refs.deployedCommit);
    setBaselineReady(true);
  };
  const clearRepositoryBaseline = () => {
    setRepositoryBranches([]);
    setProductionBranch("");
    setDeployedCommit("");
    setBaselineReady(false);
  };
  const probeRepository = useMutation({
    mutationFn: (input: { remoteUrl: string; transport: "https" | "ssh"; credentialSecretId: string }) => api.probeRepositoryRefs(project.key, input),
    onSuccess: applyRepositoryRefs,
  });
  const probeLLMModels = useMutation({
    mutationFn: (input: { baseUrl: string; credentialSecretId: string }) => api.probeLLMModels(project.key, input),
    onSuccess: (result) => {
      setLlmModels(result.models);
      if (llmModel && !result.models.includes(llmModel)) {
        setLlmModel("");
        setLlmChatReady(false);
      }
    },
  });
  const probeLLMChat = useMutation({
    mutationFn: (input: { baseUrl: string; credentialSecretId: string; model: string }) => api.probeLLMChat(project.key, input),
    onSuccess: () => setLlmChatReady(true),
  });
  const saveConfiguration = useMutation({
    mutationFn: (configuration: ProjectConfiguration) => api.putConfiguration(project.key, configuration),
    onSuccess: (saved) => {
      queryClient.setQueryData(queryKeys.configuration(project.key), saved);
      setInboundUrl(saved.trigger.inboundUrl ?? "");
    },
  });
  const rotateWebhookToken = useMutation({
    mutationFn: () => api.rotateWebhookToken(project.key),
    onSuccess: (result) => {
      setInboundUrl(result.inboundUrl);
      queryClient.setQueryData<ProjectConfiguration>(queryKeys.configuration(project.key), (currentConfiguration) => currentConfiguration ? {
        ...currentConfiguration,
        trigger: { ...currentConfiguration.trigger, inboundUrl: result.inboundUrl },
      } : currentConfiguration);
    },
  });
  const createCredential = (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => createSecret.mutateAsync(input);
  const updateCredential = (input: { secretId: string; name: string; value?: string }) => updateSecret.mutateAsync(input);
  const knownSecrets = queryClient.getQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key))?.items ?? secrets;
  const toggleCapability = (capability: string) => setSourceCapabilities((values) => values.includes(capability) ? values.filter((value) => value !== capability) : [...values, capability]);
  const onSourceKindChange = (kind: SourceKind) => {
    setSourceKind(kind);
    setSourceCapabilities(defaultSourceCapabilities(kind));
  };

  const buildConfigurationPayload = (): ProjectConfiguration => ({
    environment: { key: environmentKey.trim(), name: environmentName.trim(), service: service.trim() || null },
    repository: { remoteUrl: remoteUrl.trim(), scmProvider, transport, credentialSecretId: repositorySecretId || null, productionBranch: productionBranch.trim(), deployedCommit: deployedCommit.trim() },
    source: {
      name: sourceName.trim(), kind: sourceKind, credentialSecretId: sourceCredentialId || null,
      config: buildSourceConfig(sourceKind, { endpoint: sourceEndpoint, mcpTransport, mcpHeaders, evidenceProfile, queryScope, host: sourceHost, port: sshPort, user: sshUser, projectFolder, logPath, readMode, cloudProvider, cloudRegion, resource: sourceResource }),
      capabilities: sourceCapabilities, enabled: current?.source.enabled ?? true,
    },
    trigger: {
      name: triggerName.trim(), kind: triggerKind, signingSecretId: null,
      config: buildTriggerConfig(triggerKind, { eventTypes: "alarm", deduplicationKey: "title", groupingWindowSeconds, matchExpression }),
      enabled: current?.trigger.enabled ?? true,
    },
    llm: {
      provider: "openai",
      baseUrl: llmBaseUrl.trim(),
      credentialSecretId: llmCredentialId,
      model: llmModel.trim(),
    },
  });

  const submit = () => {
    setValidationError("");
    try {
      saveConfiguration.mutate(buildConfigurationPayload());
    } catch (error) {
      setValidationError(messageFromError(error));
    }
  };

  const saveDisabledReason = !repositorySecretId
    ? "Select a Git credential."
    : !baselineReady
      ? "Read the Git remote before saving."
      : sourceCapabilities.length === 0
        ? "Select at least one collection capability."
        : !llmCredentialId
            ? "Select an LLM API key credential."
            : !llmModel.trim()
              ? "Load and select an LLM model."
              : !llmChatReady
                ? "Test the selected model with hi before saving."
                : null;

  return <section className="setup-view">
    <div className="setup-header">
      <button type="button" className="back-link" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/configuration`)}><ChevronLeft size={17} />Configuration</button>
      <div className="eyebrow">{project.name}</div>
      <h1>Project configuration</h1>
      <p>Environment, Git baseline, one collection source, one trigger, and one LLM provider are saved as one transactional snapshot.</p>
    </div>
    <div className="wizard-steps" role="tablist">
      {STEPS.map((step) => <button
        key={step.id} type="button" role="tab" aria-selected={activeStep === step.id}
        className={activeStep === step.id ? "wizard-step-tab active" : "wizard-step-tab"}
        onClick={() => setActiveStep(step.id)}
      >{step.label}</button>)}
    </div>
    <div className="real-config-form">
      {activeStep === "repository" && <RepositoryStep
        remoteUrl={remoteUrl} setRemoteUrl={(value) => { setRemoteUrl(value); clearRepositoryBaseline(); }}
        scmProvider={scmProvider} setScmProvider={setScmProvider}
        transport={transport} setTransport={(value) => { setTransport(value); clearRepositoryBaseline(); }}
        repositorySecretId={repositorySecretId} setRepositorySecretId={(value) => { setRepositorySecretId(value); clearRepositoryBaseline(); }}
        productionBranch={productionBranch} deployedCommit={deployedCommit} branches={repositoryBranches}
        onSelectBranch={(name, commit) => { setProductionBranch(name); setDeployedCommit(commit); setBaselineReady(true); }}
        onReadRemote={() => { if (repositorySecretId) probeRepository.mutate({ remoteUrl: remoteUrl.trim(), transport, credentialSecretId: repositorySecretId }); }}
        readingRemote={probeRepository.isPending} readRemoteError={probeRepository.error}
        knownSecrets={knownSecrets} createCredential={createCredential} creatingCredential={createSecret.isPending} createCredentialError={createSecret.error}
        updateCredential={updateCredential} updatingCredential={updateSecret.isPending} updateCredentialError={updateSecret.error}
      />}
      {activeStep === "source" && <SourceStep
        sourceName={sourceName} setSourceName={setSourceName} sourceKind={sourceKind} onSourceKindChange={onSourceKindChange}
        sourceCredentialId={sourceCredentialId} setSourceCredentialId={setSourceCredentialId}
        sourceCapabilities={sourceCapabilities} toggleCapability={toggleCapability}
        sourceEndpoint={sourceEndpoint} setSourceEndpoint={setSourceEndpoint} mcpTransport={mcpTransport} setMcpTransport={setMcpTransport}
        mcpHeaders={mcpHeaders} setMcpHeaders={setMcpHeaders} evidenceProfile={evidenceProfile} setEvidenceProfile={setEvidenceProfile}
        queryScope={queryScope} setQueryScope={setQueryScope} sourceHost={sourceHost} setSourceHost={setSourceHost}
        sshPort={sshPort} setSshPort={setSshPort} sshUser={sshUser} setSshUser={setSshUser}
        projectFolder={projectFolder} setProjectFolder={setProjectFolder} logPath={logPath} setLogPath={setLogPath}
        readMode={readMode} setReadMode={setReadMode} cloudProvider={cloudProvider} setCloudProvider={setCloudProvider}
        cloudRegion={cloudRegion} setCloudRegion={setCloudRegion} sourceResource={sourceResource} setSourceResource={setSourceResource}
        knownSecrets={knownSecrets} createCredential={createCredential} creatingCredential={createSecret.isPending} createCredentialError={createSecret.error}
        updateCredential={updateCredential} updatingCredential={updateSecret.isPending} updateCredentialError={updateSecret.error}
      />}
      {activeStep === "trigger" && <TriggerStep
        triggerName={triggerName} setTriggerName={setTriggerName} triggerKind={triggerKind} setTriggerKind={setTriggerKind}
        groupingWindowSeconds={groupingWindowSeconds} setGroupingWindowSeconds={setGroupingWindowSeconds}
        matchExpression={matchExpression} setMatchExpression={setMatchExpression}
        inboundUrl={inboundUrl} onGenerateInboundUrl={() => rotateWebhookToken.mutateAsync().then((result) => result.inboundUrl)}
        generatingInboundUrl={rotateWebhookToken.isPending} generateInboundUrlError={rotateWebhookToken.error}
      />}
      {activeStep === "llm" && <LLMStep
        baseUrl={llmBaseUrl} setBaseUrl={(value) => { setLlmBaseUrl(value); setLlmModels(llmModel ? [llmModel] : []); setLlmChatReady(false); }}
        credentialId={llmCredentialId} setCredentialId={(value) => { setLlmCredentialId(value); setLlmModels(llmModel ? [llmModel] : []); setLlmChatReady(false); }}
        model={llmModel} setModel={(value) => { setLlmModel(value); setLlmChatReady(false); }} models={llmModels}
        onLoadModels={() => { if (llmCredentialId) probeLLMModels.mutate({ baseUrl: llmBaseUrl.trim(), credentialSecretId: llmCredentialId }); }}
        loadingModels={probeLLMModels.isPending} loadModelsError={probeLLMModels.error}
        onTestChat={() => { if (llmCredentialId && llmModel.trim()) probeLLMChat.mutate({ baseUrl: llmBaseUrl.trim(), credentialSecretId: llmCredentialId, model: llmModel.trim() }); }}
        testingChat={probeLLMChat.isPending} testChatError={probeLLMChat.error} chatReady={llmChatReady}
        knownSecrets={knownSecrets} createCredential={createCredential} creatingCredential={createSecret.isPending} createCredentialError={createSecret.error}
        updateCredential={updateCredential} updatingCredential={updateSecret.isPending} updateCredentialError={updateSecret.error}
      />}
      {activeStep === "review" && <ReviewStep configuration={buildConfigurationPayload()} inboundUrl={inboundUrl} />}
      {validationError && <ErrorNotice message={validationError} />}
      {saveConfiguration.error && <ErrorNotice message={messageFromError(saveConfiguration.error)} />}
      <footer className="setup-footer">
        <button className="secondary-button" type="button" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/configuration`)}>Cancel</button>
        <div className="setup-footer-status">
          {saveDisabledReason && <span className="setup-footer-reason">{saveDisabledReason}</span>}
          {saveConfiguration.isSuccess && !saveDisabledReason && <span className="setup-footer-saved" role="status">Configuration saved.</span>}
          <button className="primary-button" type="button" disabled={saveConfiguration.isPending || saveDisabledReason !== null} onClick={submit}>
            {saveConfiguration.isPending ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Save configuration
          </button>
        </div>
      </footer>
    </div>
  </section>;
}
