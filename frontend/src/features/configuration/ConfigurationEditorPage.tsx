import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight } from "lucide-react";
import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { api, messageFromError, type DockerContainer, type ListResult, type LogProbeStatus, type ProjectConfigurationDraft, type ProjectSecret, type RepositoryRefs, type SourceKind, type TriggerKind } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { LoadingState, PageError } from "../../shared/ui";
import {
  buildSourceConfig, buildTriggerConfig, defaultSourceCapabilities, isLogProbeMonitoring,
  readConfigNumber, readConfigObject, readConfigString, readCustomRules, readStringRecord, type CustomRuleDraft, type WebhookProvider,
} from "./configuration";
import { LLMStep } from "./wizard/LLMStep";
import { RemediationStep } from "./wizard/RemediationStep";
import { RepositoryStep } from "./wizard/RepositoryStep";
import { ReviewStep } from "./wizard/ReviewStep";
import { SourceStep } from "./wizard/SourceStep";
import { TriggerStep } from "./wizard/TriggerStep";

type StepId = "repository" | "source" | "trigger" | "llm" | "remediation" | "review";

function readWebhookProvider(config: Record<string, unknown> | undefined): WebhookProvider {
  const provider = readConfigString(config, "provider", "generic");
  return provider === "tencent_cls" || provider === "aws_cloudwatch" ? provider : "generic";
}

const STEPS: { id: StepId; label: string; stepNumber: number }[] = [
  { id: "repository", label: "Git repository", stepNumber: 1 },
  { id: "source", label: "Collection source", stepNumber: 2 },
  { id: "trigger", label: "Trigger", stepNumber: 3 },
  { id: "llm", label: "LLM provider", stepNumber: 4 },
  { id: "remediation", label: "Automatic hotfix", stepNumber: 5 },
  { id: "review", label: "Review", stepNumber: 6 },
];

export function ConfigurationEditorPage() {
  const project = useCurrentProject();
  const navigate = useNavigate();
  const current = useQuery({
    queryKey: queryKeys.configurationDraft(project.key),
    queryFn: ({ signal }) => api.getConfigurationDraft(project.key, signal),
  });
  const secrets = useQuery({
    queryKey: queryKeys.secrets(project.key),
    queryFn: ({ signal }) => api.listSecrets(project.key, signal),
  });
  if (current.isPending || secrets.isPending) return <LoadingState label="Loading configuration editor" />;
  if (current.isError) return <PageError message={messageFromError(current.error)} onRetry={() => void current.refetch()} />;
  if (secrets.isError) return <PageError message={messageFromError(secrets.error)} onRetry={() => void secrets.refetch()} />;
  return <ConfigurationWizard current={current.data} secrets={secrets.data.items} onCancel={() => navigate(`/projects/${encodeURIComponent(project.key)}/configuration`)} />;
}

function ConfigurationWizard({ current, secrets, onCancel }: { current: ProjectConfigurationDraft; secrets: ProjectSecret[]; onCancel: () => void }) {
  const project = useCurrentProject();
  const queryClient = useQueryClient();
  const [activeStep, setActiveStep] = useState<StepId>("repository");
  const [remoteUrl, setRemoteUrl] = useState(current.repository?.remoteUrl ?? "https://git.example.internal/platform/service.git");
  const [scmProvider, setScmProvider] = useState<NonNullable<ProjectConfigurationDraft["repository"]>["scmProvider"]>(current.repository?.scmProvider ?? "generic");
  const [transport, setTransport] = useState<NonNullable<ProjectConfigurationDraft["repository"]>["transport"]>(current.repository?.transport ?? "https");
  const [repositorySecretId, setRepositorySecretId] = useState(current.repository?.credentialSecretId ?? "");
  const [productionBranch, setProductionBranch] = useState(current.repository?.productionBranch ?? "");
  const [deployedCommit, setDeployedCommit] = useState(current.repository?.deployedCommit ?? "");
  const [repositoryBranches, setRepositoryBranches] = useState<{ name: string; commit: string }[]>(
    current.repository?.productionBranch && current.repository.deployedCommit
      ? [{ name: current.repository.productionBranch, commit: current.repository.deployedCommit }]
      : [],
  );
  const [baselineReady, setBaselineReady] = useState(Boolean(current.repository?.productionBranch && current.repository.deployedCommit));
  const [sourceKind, setSourceKind] = useState<SourceKind>(current.source?.kind ?? "cloud");
  const [sourceCredentialId, setSourceCredentialId] = useState(current.source?.credentialSecretId ?? "");
  const [sourceCapabilities, setSourceCapabilities] = useState(current.source?.capabilities ?? defaultSourceCapabilities(current.source?.kind ?? "cloud"));
  const [sourceEndpoint, setSourceEndpoint] = useState(readConfigString(current.source?.config, "endpoint", "https://mcp.example.internal/mcp"));
  const [mcpTransport, setMcpTransport] = useState(readConfigString(current.source?.config, "transport", "streamable_http"));
  const [mcpHeaders, setMcpHeaders] = useState(JSON.stringify(readStringRecord(current.source?.config, "headers"), null, 2));
  const [evidenceProfile, setEvidenceProfile] = useState(readConfigString(current.source?.config, "evidenceProfile", "errors-context"));
  const [queryScope, setQueryScope] = useState(readConfigString(current.source?.config, "queryScope", "project"));
  const [sourceHost, setSourceHost] = useState(readConfigString(current.source?.config, "host", "api-prod.internal"));
  const [sshPort, setSshPort] = useState(readConfigNumber(current.source?.config, "port", 22));
  const [sshUser, setSshUser] = useState(readConfigString(current.source?.config, "user", "collector"));
  const [projectFolder, setProjectFolder] = useState(readConfigString(current.source?.config, "projectFolder", "/srv/app"));
  const [logPath, setLogPath] = useState(readConfigString(current.source?.config, "logPath", "/var/log/app.log"));
  const [readMode, setReadMode] = useState(readConfigString(current.source?.config, "mode", "tail"));
  const sourceDeployment = readConfigObject(current.source?.config, "deployment");
  const [sshDeploymentKind, setSshDeploymentKind] = useState<"host" | "docker">(readConfigString(sourceDeployment, "kind", "host") === "docker" ? "docker" : "host");
  const [sshContainerName, setSshContainerName] = useState(readConfigString(sourceDeployment, "containerName", ""));
  const [dockerContainers, setDockerContainers] = useState<DockerContainer[]>([]);
  const [cloudProvider, setCloudProvider] = useState(readConfigString(current.source?.config, "provider", "generic"));
  const [cloudRegion, setCloudRegion] = useState(readConfigString(current.source?.config, "region", "default"));
  const [sourceResource, setSourceResource] = useState(readConfigString(current.source?.config, "resource", "service-logs"));
  const [triggerKind, setTriggerKind] = useState<TriggerKind>(current.trigger?.kind ?? "custom_rule");
  const [savedTriggerKind, setSavedTriggerKind] = useState<TriggerKind | null>(current.trigger?.kind ?? null);
  const [inboundUrl, setInboundUrl] = useState(current.trigger?.inboundUrl ?? "");
  const [webhookProvider, setWebhookProvider] = useState<WebhookProvider>(readWebhookProvider(current.trigger?.config));
  const awsCloudWatchConfig = readConfigObject(current.trigger?.config, "awsCloudWatch");
  const [awsTopicArn, setAwsTopicArn] = useState(readConfigString(awsCloudWatchConfig, "topicArn", ""));
  const [groupingWindowSeconds, setGroupingWindowSeconds] = useState(readConfigNumber(current.trigger?.config, "groupingWindowSeconds", 900));
  const [customRules, setCustomRules] = useState<CustomRuleDraft[]>(readCustomRules(current.trigger?.config));
  const [ruleIntent, setRuleIntent] = useState("");
  const [ruleSample, setRuleSample] = useState("");
  const [llmBaseUrl, setLlmBaseUrl] = useState(current.llm?.baseUrl ?? "https://api.openai.com");
  const [llmCredentialId, setLlmCredentialId] = useState(current.llm?.credentialSecretId ?? "");
  const [llmModel, setLlmModel] = useState(current.llm?.model ?? "");
  const [llmModels, setLlmModels] = useState<string[]>(current.llm?.model ? [current.llm.model] : []);
  const [llmChatReady, setLlmChatReady] = useState(false);
  const [remediationPolicy, setRemediationPolicy] = useState<NonNullable<ProjectConfigurationDraft["remediation"]>>(current.remediation ?? {
    agentLoopMode: "legacy",
    executionMode: "analysis_only",
    validationProfile: {
      enabled: false, imageDigest: "", workingDirectory: ".", preparation: [], requiredCommands: [],
      cpuLimit: 2, memoryLimitMiB: 4096, workspaceLimitMiB: 10240,
    },
    publication: { branchPrefix: "hotfix/remediation", gitCredentialSecretId: "", apiCredentialSecretId: "", apiBaseUrl: "" },
    changePolicy: { allowedPaths: ["**"], deniedPaths: [], maxChangedFiles: 10, maxChangedLines: 400 },
  });

  const updateDraft = (next: Partial<ProjectConfigurationDraft>) => {
    queryClient.setQueryData<ProjectConfigurationDraft>(queryKeys.configurationDraft(project.key), (draft) => ({
      environment: null,
      repository: null,
      source: null,
      trigger: null,
      llm: null,
      remediation: null,
      ...(draft ?? {}),
      ...next,
    }));
    void queryClient.invalidateQueries({ queryKey: queryKeys.configuration(project.key) });
  };

  const buildRepositoryPayload = () => ({
    remoteUrl: remoteUrl.trim(), scmProvider, transport, credentialSecretId: repositorySecretId || null,
    productionBranch: productionBranch.trim(), deployedCommit: deployedCommit.trim(),
  });
  const buildSourcePayload = () => ({
    kind: sourceKind, credentialSecretId: sourceCredentialId || null,
    config: buildSourceConfig(sourceKind, { endpoint: sourceEndpoint, mcpTransport, mcpHeaders, evidenceProfile, queryScope, host: sourceHost, port: sshPort, user: sshUser, projectFolder, logPath, readMode, cloudProvider, cloudRegion, resource: sourceResource, sshDeploymentKind, sshContainerName }),
    capabilities: sourceCapabilities, enabled: current.source?.enabled ?? true,
  });
  const buildTriggerPayload = () => ({
    kind: triggerKind, signingSecretId: null,
    config: buildTriggerConfig(triggerKind, { eventTypes: "alarm", deduplicationKey: "title", groupingWindowSeconds, matchExpression: "", customRules, webhookProvider, awsTopicArn }),
    enabled: current.trigger?.enabled ?? true,
  });
  const buildLLMPayload = () => ({ provider: "openai" as const, baseUrl: llmBaseUrl.trim(), credentialSecretId: llmCredentialId, model: llmModel.trim() });
  const buildRemediationPayload = () => {
    const payload = { ...remediationPolicy };
    delete (payload as { version?: unknown }).version;
    return payload;
  };

  const createSecret = useMutation({
    mutationFn: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => api.createSecret(project.key, input),
    onSuccess: (created) => {
      queryClient.setQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key), (value) => ({
        items: [...(value?.items ?? []), created],
        total: (value?.total ?? 0) + 1,
      }));
    },
  });
  const updateSecret = useMutation({
    mutationFn: (input: { secretId: string; name: string; value?: string }) => api.updateSecret(project.key, input.secretId, { name: input.name, value: input.value }),
    onSuccess: (updated) => {
      queryClient.setQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key), (value) => value ? {
        items: value.items.map((item) => item.id === updated.id ? updated : item),
        total: value.total,
      } : value);
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
  const probeDockerContainers = useMutation({
    mutationFn: (input: { host: string; port: number; user: string; credentialSecretId: string }) => api.probeSSHContainers(project.key, input),
    onSuccess: (result) => {
      setDockerContainers(result.containers);
      setSshContainerName((selected) => result.containers.some((container) => container.name === selected) ? selected : "");
    },
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
  const saveRepository = useMutation({
    mutationFn: () => api.putConfigurationRepository(project.key, buildRepositoryPayload()),
    onSuccess: (saved) => updateDraft({ repository: saved }),
  });
  const saveSource = useMutation({
    mutationFn: () => api.putConfigurationSource(project.key, buildSourcePayload()),
    onSuccess: (saved) => updateDraft({ source: saved }),
  });
  const saveTrigger = useMutation({
    mutationFn: () => api.putConfigurationTrigger(project.key, buildTriggerPayload()),
    onSuccess: (saved) => {
      updateDraft({ trigger: saved });
      setSavedTriggerKind(saved.kind);
      setInboundUrl(saved.inboundUrl ?? "");
    },
  });
  const saveLLM = useMutation({
    mutationFn: () => api.putConfigurationLLM(project.key, buildLLMPayload()),
    onSuccess: (saved) => updateDraft({ llm: saved }),
  });
  const saveRemediation = useMutation({
    mutationFn: () => api.putConfigurationRemediation(project.key, buildRemediationPayload()),
    onSuccess: (saved) => {
      setRemediationPolicy(saved);
      updateDraft({ remediation: saved });
    },
  });
  const rotateWebhookToken = useMutation({
    mutationFn: () => api.rotateWebhookToken(project.key),
    onSuccess: (result) => {
      setInboundUrl(result.inboundUrl);
      updateDraft({ trigger: current.trigger ? { ...current.trigger, inboundUrl: result.inboundUrl } : current.trigger });
    },
  });
  const generateLogRule = useMutation({
    mutationFn: () => api.generateLogRule(project.key, { intent: ruleIntent.trim(), sample: ruleSample.trim() || "No log sample provided." }),
    onSuccess: (rule) => setCustomRules((rules) => {
      const starter = rules.length === 1 && rules[0].id === "errors" && rules[0].name === "Application errors" && rules[0].pattern === "level=ERROR";
      if (starter) return [rule];
      const used = new Set(rules.map((item) => item.id));
      let id = rule.id;
      let suffix = 2;
      while (used.has(id)) {
        id = `${rule.id}-${suffix}`;
        suffix += 1;
      }
      return [...rules, { ...rule, id }];
    }),
  });
  const [pendingProbe, setPendingProbe] = useState<LogProbeStatus | null>(null);
  const [probeTimedOut, setProbeTimedOut] = useState(false);
  const probeQueryKey = ["log-probe", project.key];
  const probeStatus = useQuery({
    queryKey: probeQueryKey,
    queryFn: ({ signal }) => api.getLogProbeStatus(project.key, signal),
    enabled: savedTriggerKind === "custom_rule" && (activeStep === "trigger" || pendingProbe !== null),
    retry: false,
    refetchInterval: (query) => pendingProbe && !probeTimedOut && !isLogProbeMonitoring(query.state.data, pendingProbe) ? 3000 : false,
  });
  const probeHealthy = isLogProbeMonitoring(probeStatus.data, pendingProbe ?? undefined);
  const probeChecking = pendingProbe !== null && !probeHealthy && !probeTimedOut;
  useEffect(() => {
    if (!pendingProbe || probeHealthy) return;
    const timer = window.setTimeout(() => setProbeTimedOut(true), 90_000);
    return () => window.clearTimeout(timer);
  }, [pendingProbe, probeHealthy]);
  const installLogProbe = useMutation({
    mutationFn: () => api.installLogProbe(project.key),
    onSuccess: (status) => {
      setProbeTimedOut(false);
      setPendingProbe(status);
      queryClient.setQueryData(probeQueryKey, status);
      void queryClient.invalidateQueries({ queryKey: probeQueryKey });
    },
  });
  const refreshLogProbe = useMutation({
    mutationFn: () => api.getLogProbeStatus(project.key),
    onSuccess: (status) => queryClient.setQueryData(probeQueryKey, status),
  });
  const uninstallLogProbe = useMutation({
    mutationFn: () => api.uninstallLogProbe(project.key),
    onSuccess: (status) => {
      setPendingProbe(null);
      setProbeTimedOut(false);
      queryClient.setQueryData(probeQueryKey, status);
    },
  });
  const createCredential = (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => createSecret.mutateAsync(input);
  const updateCredential = (input: { secretId: string; name: string; value?: string }) => updateSecret.mutateAsync(input);
  const knownSecrets = queryClient.getQueryData<ListResult<ProjectSecret>>(queryKeys.secrets(project.key))?.items ?? secrets;
  const clearDockerDiscovery = () => {
    setDockerContainers([]);
    setSshContainerName("");
    probeDockerContainers.reset();
  };
  const toggleCapability = (capability: string) => setSourceCapabilities((values) => values.includes(capability) ? values.filter((value) => value !== capability) : [...values, capability]);
  const onSourceKindChange = (kind: SourceKind) => {
    setSourceKind(kind);
    setSourceCapabilities(defaultSourceCapabilities(kind));
    clearDockerDiscovery();
  };

  const currentStepIndex = STEPS.findIndex((step) => step.id === activeStep);
  const prevStep = currentStepIndex > 0 ? STEPS[currentStepIndex - 1] : null;
  const nextStep = currentStepIndex < STEPS.length - 1 ? STEPS[currentStepIndex + 1] : null;

  return <section className="setup-view">
    <div className="setup-header">
      <button type="button" className="back-link" onClick={onCancel}>
        <ChevronLeft size={16} />
        <span>Configuration</span>
      </button>
      <h1>Project configuration</h1>
      <p className="setup-header-desc">
        Configure repository baseline, telemetry collection, alerts, and automatic remediation policies.
      </p>
    </div>
    <div className="wizard-steps" role="tablist">
      {STEPS.map((step) => {
        const isCurrent = activeStep === step.id;
        const isCompleted = step.stepNumber < (STEPS.find((s) => s.id === activeStep)?.stepNumber ?? 1);
        return (
          <button
            key={step.id}
            type="button"
            role="tab"
            aria-selected={isCurrent}
            className={`wizard-step-tab ${isCurrent ? "active" : ""} ${isCompleted ? "completed" : ""}`}
            onClick={() => setActiveStep(step.id)}
          >
            <span className="wizard-step-num">
              {isCompleted ? <Check size={12} strokeWidth={2.5} /> : step.stepNumber}
            </span>
            <span className="wizard-step-name">{step.label}</span>
          </button>
        );
      })}
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
        onSave={() => saveRepository.mutate()} saving={saveRepository.isPending} canSave={Boolean(repositorySecretId && baselineReady)} saveError={saveRepository.error}
        knownSecrets={knownSecrets} createCredential={createCredential} creatingCredential={createSecret.isPending} createCredentialError={createSecret.error}
        updateCredential={updateCredential} updatingCredential={updateSecret.isPending} updateCredentialError={updateSecret.error}
      />}
      {activeStep === "source" && <SourceStep
        sourceKind={sourceKind} onSourceKindChange={onSourceKindChange}
        sourceCredentialId={sourceCredentialId} setSourceCredentialId={(value) => { setSourceCredentialId(value); clearDockerDiscovery(); }}
        sourceCapabilities={sourceCapabilities} toggleCapability={toggleCapability}
        sourceEndpoint={sourceEndpoint} setSourceEndpoint={setSourceEndpoint} mcpTransport={mcpTransport} setMcpTransport={setMcpTransport}
        mcpHeaders={mcpHeaders} setMcpHeaders={setMcpHeaders} evidenceProfile={evidenceProfile} setEvidenceProfile={setEvidenceProfile}
        queryScope={queryScope} setQueryScope={setQueryScope} sourceHost={sourceHost} setSourceHost={(value) => { setSourceHost(value); clearDockerDiscovery(); }}
        sshPort={sshPort} setSshPort={(value) => { setSshPort(value); clearDockerDiscovery(); }} sshUser={sshUser} setSshUser={(value) => { setSshUser(value); clearDockerDiscovery(); }}
        projectFolder={projectFolder} setProjectFolder={setProjectFolder} logPath={logPath} setLogPath={setLogPath}
        readMode={readMode} setReadMode={setReadMode} cloudProvider={cloudProvider} setCloudProvider={setCloudProvider}
        cloudRegion={cloudRegion} setCloudRegion={setCloudRegion} sourceResource={sourceResource} setSourceResource={setSourceResource}
        sshDeploymentKind={sshDeploymentKind} setSshDeploymentKind={(value) => { setSshDeploymentKind(value); clearDockerDiscovery(); }}
        sshContainerName={sshContainerName} setSshContainerName={setSshContainerName}
        dockerContainers={dockerContainers}
        onRefreshDockerContainers={() => { if (sourceCredentialId) probeDockerContainers.mutate({ host: sourceHost.trim(), port: sshPort, user: sshUser.trim(), credentialSecretId: sourceCredentialId }); }}
        refreshingDockerContainers={probeDockerContainers.isPending} dockerContainerError={probeDockerContainers.error}
        onSave={() => saveSource.mutate()} saving={saveSource.isPending}
        canSave={sourceCapabilities.length > 0 && (sourceKind !== "ssh" || sshDeploymentKind !== "docker" || dockerContainers.some((container) => container.name === sshContainerName))}
        saveError={saveSource.error}
        knownSecrets={knownSecrets} createCredential={createCredential} creatingCredential={createSecret.isPending} createCredentialError={createSecret.error}
        updateCredential={updateCredential} updatingCredential={updateSecret.isPending} updateCredentialError={updateSecret.error}
      />}
      {activeStep === "trigger" && <TriggerStep
        triggerKind={triggerKind} setTriggerKind={setTriggerKind}
        webhookProvider={webhookProvider} setWebhookProvider={setWebhookProvider}
        awsTopicArn={awsTopicArn} setAwsTopicArn={setAwsTopicArn}
        groupingWindowSeconds={groupingWindowSeconds} setGroupingWindowSeconds={setGroupingWindowSeconds}
        customRules={customRules} setCustomRules={setCustomRules}
        ruleIntent={ruleIntent} setRuleIntent={setRuleIntent} ruleSample={ruleSample} setRuleSample={setRuleSample}
        onGenerateRule={() => generateLogRule.mutate()} generatingRule={generateLogRule.isPending} generateRuleError={generateLogRule.error}
        logProbeStatus={probeChecking ? pendingProbe ?? undefined : probeStatus.data ?? installLogProbe.data ?? uninstallLogProbe.data}
        probeChecking={probeChecking} probeTimedOut={pendingProbe !== null && probeTimedOut && !probeHealthy}
        onInstallLogProbe={() => installLogProbe.mutate()} installingLogProbe={installLogProbe.isPending} installLogProbeError={installLogProbe.error}
        onRefreshLogProbe={() => refreshLogProbe.mutate()} refreshingLogProbe={refreshLogProbe.isPending} refreshLogProbeError={refreshLogProbe.error ?? (probeChecking ? probeStatus.error : undefined)}
        onUninstallLogProbe={() => uninstallLogProbe.mutate()} uninstallingLogProbe={uninstallLogProbe.isPending} uninstallLogProbeError={uninstallLogProbe.error}
        canManageLogProbe={sourceKind === "ssh" && sshDeploymentKind === "host" && readMode === "tail" && savedTriggerKind === "custom_rule"}
        inboundUrl={inboundUrl} onGenerateInboundUrl={() => rotateWebhookToken.mutateAsync().then((result) => result.inboundUrl)}
        generatingInboundUrl={rotateWebhookToken.isPending} generateInboundUrlError={rotateWebhookToken.error}
        canGenerateInboundUrl={savedTriggerKind === "signed_webhook"}
        onSave={() => saveTrigger.mutate()} saving={saveTrigger.isPending}
        canSave={(triggerKind !== "custom_rule" || (customRules.length > 0 && new Set(customRules.map((rule) => rule.id.trim())).size === customRules.length && customRules.every((rule) => rule.id.trim() && rule.name.trim() && rule.pattern.trim() && rule.threshold >= 1 && rule.threshold <= 10000 && rule.windowSeconds >= 1 && rule.windowSeconds <= 86400 && rule.cooldownSeconds >= 0 && rule.cooldownSeconds <= 86400))) && (webhookProvider !== "aws_cloudwatch" || /^arn:aws:sns:[a-z0-9-]+:[0-9]{12}:[A-Za-z0-9_-]+$/.test(awsTopicArn.trim()))}
        saveError={saveTrigger.error}
      />}
      {activeStep === "llm" && <LLMStep
        baseUrl={llmBaseUrl} setBaseUrl={(value) => { setLlmBaseUrl(value); setLlmModels(llmModel ? [llmModel] : []); setLlmChatReady(false); }}
        credentialId={llmCredentialId} setCredentialId={(value) => { setLlmCredentialId(value); setLlmModels(llmModel ? [llmModel] : []); setLlmChatReady(false); }}
        model={llmModel} setModel={(value) => { setLlmModel(value); setLlmChatReady(false); }} models={llmModels}
        onLoadModels={() => { if (llmCredentialId) probeLLMModels.mutate({ baseUrl: llmBaseUrl.trim(), credentialSecretId: llmCredentialId }); }}
        loadingModels={probeLLMModels.isPending} loadModelsError={probeLLMModels.error}
        onTestChat={() => { if (llmCredentialId && llmModel.trim()) probeLLMChat.mutate({ baseUrl: llmBaseUrl.trim(), credentialSecretId: llmCredentialId, model: llmModel.trim() }); }}
        testingChat={probeLLMChat.isPending} testChatError={probeLLMChat.error} chatReady={llmChatReady}
        onSave={() => saveLLM.mutate()} saving={saveLLM.isPending} canSave={Boolean(llmCredentialId && llmModel.trim() && llmChatReady)} saveError={saveLLM.error}
        knownSecrets={knownSecrets} createCredential={createCredential} creatingCredential={createSecret.isPending} createCredentialError={createSecret.error}
        updateCredential={updateCredential} updatingCredential={updateSecret.isPending} updateCredentialError={updateSecret.error}
      />}
      {activeStep === "remediation" && <RemediationStep
        policy={remediationPolicy} setPolicy={setRemediationPolicy} secrets={knownSecrets}
        onSave={() => saveRemediation.mutate()} saving={saveRemediation.isPending} saveError={saveRemediation.error}
      />}
      {activeStep === "review" && <ReviewStep configuration={current} inboundUrl={inboundUrl} />}
      <footer className="setup-footer">
        <div className="setup-footer-left">
          <button className="secondary-button" type="button" onClick={onCancel}>
            <ChevronLeft size={15} />
            <span>Return to configuration</span>
          </button>
        </div>
        <div className="setup-footer-nav">
          {prevStep && (
            <button
              type="button"
              className="secondary-button"
              onClick={() => setActiveStep(prevStep.id)}
            >
              <ChevronLeft size={15} />
              <span>{prevStep.label}</span>
            </button>
          )}
          {nextStep && (
            <button
              type="button"
              className="secondary-button"
              onClick={() => setActiveStep(nextStep.id)}
            >
              <span>{nextStep.label}</span>
              <ChevronRight size={15} />
            </button>
          )}
          {activeStep === "review" && (
            <button
              type="button"
              className="primary-button"
              onClick={onCancel}
            >
              <Check size={15} />
              <span>Done</span>
            </button>
          )}
        </div>
      </footer>
    </div>
  </section>;
}
