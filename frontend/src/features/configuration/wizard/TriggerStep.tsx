import { Activity, Check, Copy, FlaskConical, LoaderCircle, Plus, RefreshCw, ScrollText, ServerCog, Trash2, Unplug, WandSparkles, Webhook } from "lucide-react";
import { useState, type ReactNode } from "react";
import { messageFromError, type LogProbeStatus, type LogRuleTrial, type TriggerKind } from "../../../api";
import type { CustomRuleDraft, WebhookProvider } from "../configuration";

const COMMON_WINDOWS = [60, 300, 900, 1800, 3600];

const TRIGGER_KINDS: { id: TriggerKind; label: string; description: string; icon: ReactNode }[] = [
  { id: "custom_rule", label: "Managed log probe", description: "Mendry watches the log file over SSH and raises incidents itself.", icon: <ScrollText size={17} /> },
  { id: "signed_webhook", label: "Signed webhook", description: "An external alerting system posts signed events to Mendry.", icon: <Webhook size={17} /> },
];

function durationLabel(seconds: number): string {
  if (seconds === 0) return "No delay";
  if (seconds < 60) return `${seconds} sec`;
  if (seconds % 3600 === 0) return `${seconds / 3600} hr`;
  return `${seconds / 60} min`;
}

function frequencyLabel(rule: CustomRuleDraft): string {
  const frequency = rule.threshold === 1 ? "once" : `${rule.threshold} times`;
  return `${frequency} within ${durationLabel(rule.windowSeconds).toLowerCase()}`;
}

function probeStatusLabel(status: LogProbeStatus | undefined): string {
  if (status?.state === "active" && status.message === "monitoring") return "Monitoring enabled";
  if (status?.state === "not_installed") return "Monitoring disabled";
  if (status?.state === "starting") return "Starting monitoring";
  if (status?.state === "active") return "Monitoring needs attention";
  return "Monitoring status unknown";
}

export function TriggerStep({
  triggerKind, setTriggerKind,
  webhookProvider, setWebhookProvider,
  awsTopicArn, setAwsTopicArn,
  groupingWindowSeconds, setGroupingWindowSeconds, customRules, setCustomRules,
  ruleIntent, setRuleIntent, ruleSample, setRuleSample, onGenerateRule, generatingRule = false, generateRuleError,
  onTrialLogRules,
  inboundUrl, onGenerateInboundUrl, generatingInboundUrl = false, generateInboundUrlError,
  canGenerateInboundUrl = false,
  logProbeStatus, probeChecking = false, probeTimedOut = false, onInstallLogProbe, installingLogProbe = false, installLogProbeError,
  onRefreshLogProbe, refreshingLogProbe = false, refreshLogProbeError,
  onUninstallLogProbe, uninstallingLogProbe = false, uninstallLogProbeError, canManageLogProbe = false,
  onSave, saving = false, canSave = false, saveError,
}: {
  triggerKind: TriggerKind;
  setTriggerKind: (value: TriggerKind) => void;
  webhookProvider: WebhookProvider;
  setWebhookProvider: (value: WebhookProvider) => void;
  awsTopicArn: string;
  setAwsTopicArn: (value: string) => void;
  groupingWindowSeconds: number;
  setGroupingWindowSeconds: (value: number) => void;
  customRules: CustomRuleDraft[];
  setCustomRules: (value: CustomRuleDraft[]) => void;
  ruleIntent: string;
  setRuleIntent: (value: string) => void;
  ruleSample: string;
  setRuleSample: (value: string) => void;
  onGenerateRule: () => void;
  generatingRule?: boolean;
  generateRuleError?: unknown;
  onTrialLogRules: (positive: string[], negative: string[]) => Promise<LogRuleTrial>;
  inboundUrl?: string | null;
  onGenerateInboundUrl?: () => Promise<string>;
  generatingInboundUrl?: boolean;
  generateInboundUrlError?: unknown;
  canGenerateInboundUrl?: boolean;
  logProbeStatus?: LogProbeStatus;
  probeChecking?: boolean;
  probeTimedOut?: boolean;
  onInstallLogProbe: () => void;
  installingLogProbe?: boolean;
  installLogProbeError?: unknown;
  onRefreshLogProbe: () => void;
  refreshingLogProbe?: boolean;
  refreshLogProbeError?: unknown;
  onUninstallLogProbe: () => void;
  uninstallingLogProbe?: boolean;
  uninstallLogProbeError?: unknown;
  canManageLogProbe?: boolean;
  onSave: () => void;
  saving?: boolean;
  canSave?: boolean;
  saveError?: unknown;
}) {
  const [copied, setCopied] = useState(false);
  const [trialPositive, setTrialPositive] = useState("");
  const [trialNegative, setTrialNegative] = useState("");
  const [trialPending, setTrialPending] = useState(false);
  const [trial, setTrial] = useState<{ signature: string; result?: LogRuleTrial; error?: unknown } | null>(null);
  const trialSignature = JSON.stringify([customRules, groupingWindowSeconds, trialPositive, trialNegative]);
  const trialResult = trial?.signature === trialSignature ? trial.result : undefined;
  const trialSummary = trialResult ? (() => {
    const covered = new Set(trialResult.rules.flatMap((rule) => rule.positiveMatches));
    const unexpected = [...new Set(trialResult.rules.flatMap((rule) => rule.negativeMatches))].sort((a, b) => a - b);
    const missing = Array.from({ length: trialResult.positiveCount }, (_, index) => index + 1).filter((line) => !covered.has(line));
    return { covered: covered.size, unexpected, missing };
  })() : undefined;
  const trialError = trial?.signature === trialSignature ? trial.error : undefined;
  const sampleLines = (text: string) => text.split(/\r?\n/).map((line) => line.replace(/\r$/, "")).filter((line) => line.trim() !== "");
  const runTrial = async () => {
    const signature = trialSignature;
    setTrialPending(true);
    setTrial(null);
    try {
      const result = await onTrialLogRules(sampleLines(trialPositive), sampleLines(trialNegative));
      setTrial({ signature, result });
    } catch (error) {
      setTrial({ signature, error });
    } finally {
      setTrialPending(false);
    }
  };
  const copyUrl = async () => {
    if (!inboundUrl) return;
    await navigator.clipboard.writeText(inboundUrl);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };
  const updateRule = (index: number, patch: Partial<CustomRuleDraft>) => {
    setCustomRules(customRules.map((rule, current) => current === index ? { ...rule, ...patch } : rule));
  };
  const addRule = () => {
    const used = new Set(customRules.map((rule) => rule.id));
    let suffix = customRules.length + 1;
    while (used.has(`rule-${suffix}`)) suffix += 1;
    setCustomRules([...customRules, {
      id: `rule-${suffix}`, name: "New log rule", matchType: "contains", pattern: "ERROR",
      excludePattern: "", threshold: 1, windowSeconds: 60, cooldownSeconds: 300,
    }]);
  };
  const probeBusy = installingLogProbe || refreshingLogProbe || uninstallingLogProbe;
  const probeError = installLogProbeError ?? refreshLogProbeError ?? uninstallLogProbeError;

  return <section className="trigger-step">
    <div className="setup-card-title">
      <div className="setup-card-icon"><Activity size={18} /></div>
      <div className="setup-card-heading">
        <h2>Trigger</h2>
        <p>Choose how an incident is detected and opened.</p>
      </div>
    </div>
    <div className="trigger-kind-picker" role="radiogroup" aria-label="Trigger type">
      {TRIGGER_KINDS.map((option) => <button
        key={option.id}
        type="button"
        role="radio"
        aria-checked={triggerKind === option.id}
        className={`trigger-kind-card ${triggerKind === option.id ? "selected" : ""}`}
        onClick={() => setTriggerKind(option.id)}
      >
        <span className="trigger-kind-icon">{option.icon}</span>
        <span className="trigger-kind-text">
          <strong>{option.label}</strong>
          <small>{option.description}</small>
        </span>
        <span className="trigger-kind-check" aria-hidden="true"><Check size={12} strokeWidth={3} /></span>
      </button>)}
    </div>
    {triggerKind === "signed_webhook" ? <>
      <label>Webhook provider<select aria-label="Webhook provider" value={webhookProvider} onChange={(event) => setWebhookProvider(event.target.value as WebhookProvider)}>
        <option value="generic">Generic webhook</option>
        <option value="tencent_cls">Tencent Cloud CLS alert</option>
        <option value="aws_cloudwatch">AWS CloudWatch Alarm via SNS</option>
      </select></label>
      {webhookProvider === "aws_cloudwatch" && <label>SNS Topic ARN<input aria-label="SNS Topic ARN" value={awsTopicArn} onChange={(event) => setAwsTopicArn(event.target.value)} placeholder="arn:aws:sns:us-east-1:123456789012:mendry-alarms" /></label>}
      <div className="inbound-url-field">
        <label>Inbound webhook URL<div className="form-input-action-row">
          <input aria-label="Inbound webhook URL" value={inboundUrl ?? ""} readOnly placeholder="Generate an inbound URL after the project is saved." />
          <button className="secondary-button" type="button" disabled={!inboundUrl} onClick={() => void copyUrl()}>
            {copied ? <Check size={15} /> : <Copy size={15} />}{copied ? "Copied" : "Copy URL"}
          </button>
          {onGenerateInboundUrl && <button className="secondary-button" type="button" disabled={generatingInboundUrl || !canGenerateInboundUrl} onClick={() => void onGenerateInboundUrl()}>
            {generatingInboundUrl ? <LoaderCircle className="spin" size={15} /> : null}{inboundUrl ? "Regenerate URL" : "Generate URL"}
          </button>}
        </div></label>
        {!canGenerateInboundUrl && <p className="inbound-url-hint">Save the signed webhook configuration first. The first save creates the inbound URL.</p>}
        {generateInboundUrlError instanceof Error && <p className="credential-field-error" role="alert">{generateInboundUrlError.message}</p>}
      </div>
    </> : <>
      <div className="probe-intent-composer">
        <div className="probe-intent-head">
          <span className="probe-intent-icon"><WandSparkles size={15} /></span>
          <div>
            <strong>Describe what to watch for</strong>
            <small>Mendry drafts a matching rule from a plain-language description.</small>
          </div>
        </div>
        <textarea
          aria-label="What should Mendry watch for?"
          value={ruleIntent}
          onChange={(event) => setRuleIntent(event.target.value)}
          placeholder="Alert me when payment failures happen repeatedly within a few minutes"
        />
        <details className="probe-sample-input">
          <summary>Add a log sample</summary>
          <label>Redacted log sample<textarea value={ruleSample} onChange={(event) => setRuleSample(event.target.value)} placeholder="2026-03-01 ERROR PAYMENT_FAILED order=demo" /></label>
        </details>
        <div className="probe-intent-actions">
          <button className="primary-button probe-generate-button" type="button" disabled={generatingRule || customRules.length >= 20 || ruleIntent.trim().length < 3} onClick={onGenerateRule}>
            {generatingRule ? <LoaderCircle className="spin" size={16} /> : <WandSparkles size={16} />}Create monitoring rule
          </button>
          {generateRuleError !== undefined && generateRuleError !== null && <p className="credential-field-error" role="alert">{messageFromError(generateRuleError)}</p>}
        </div>
      </div>
      <div className="probe-rule-toolbar">
        <div className="probe-section-head">
          <h3>Rules<span className="probe-count-badge">{customRules.length}</span></h3>
          <p>Every rule is evaluated independently against incoming log lines.</p>
        </div>
        <button className="secondary-button" type="button" onClick={addRule} disabled={customRules.length >= 20}><Plus size={15} />Create manually</button>
      </div>
      <div className="probe-rule-list">
        {customRules.map((rule, index) => <article className="probe-rule" key={`${rule.id}-${index}`}>
          <div className="probe-rule-heading">
            <div className="probe-rule-summary">
              <strong>{rule.name || `Rule ${index + 1}`}</strong>
              <div className="probe-rule-meta">
                <span className="probe-rule-chip">{rule.matchType === "regex" ? "regex" : "contains"}</span>
                <code>{rule.pattern || "…"}</code>
                <span className="probe-rule-dot" aria-hidden="true" />
                <span>{frequencyLabel(rule)}</span>
              </div>
            </div>
            <button className="icon-button" type="button" title="Delete rule" aria-label={`Delete rule ${index + 1}`} disabled={customRules.length === 1} onClick={() => setCustomRules(customRules.filter((_, current) => current !== index))}><Trash2 size={15} /></button>
          </div>
          <details className="probe-rule-editor" open={rule.name === "New log rule"}>
            <summary>Edit rule</summary>
            <div className="probe-rule-fields">
              <label>Alert name<input value={rule.name} onChange={(event) => updateRule(index, { name: event.target.value })} /></label>
              <div className="probe-condition-row">
                <label>Condition<select value={rule.matchType} onChange={(event) => updateRule(index, { matchType: event.target.value as CustomRuleDraft["matchType"] })}><option value="contains">Log contains</option><option value="regex">Log matches regex</option></select></label>
                <label>Text or pattern<input value={rule.pattern} onChange={(event) => updateRule(index, { pattern: event.target.value })} /></label>
              </div>
              <div className="probe-frequency-row">
                <label>Occurrences<input type="number" min="1" max="10000" value={rule.threshold} onChange={(event) => updateRule(index, { threshold: Number(event.target.value) })} /></label>
                <label>Within<select value={rule.windowSeconds} onChange={(event) => updateRule(index, { windowSeconds: Number(event.target.value) })}>
                  {!COMMON_WINDOWS.includes(rule.windowSeconds) && <option value={rule.windowSeconds}>{durationLabel(rule.windowSeconds)}</option>}
                  {COMMON_WINDOWS.map((seconds) => <option key={seconds} value={seconds}>{durationLabel(seconds)}</option>)}
                </select></label>
              </div>
              <details className="probe-advanced-settings">
                <summary>Advanced settings</summary>
                <div className="probe-advanced-fields">
                  <label>Ignore matching<input value={rule.excludePattern} onChange={(event) => updateRule(index, { excludePattern: event.target.value })} placeholder="healthcheck|known warning" /></label>
                  <label>Wait before alerting again<select value={rule.cooldownSeconds} onChange={(event) => updateRule(index, { cooldownSeconds: Number(event.target.value) })}>
                    {[0, 60, 300, 900, 1800, 3600].map((seconds) => <option key={seconds} value={seconds}>{durationLabel(seconds)}</option>)}
                  </select></label>
                  <label>Rule ID<input value={rule.id} onChange={(event) => updateRule(index, { id: event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "-") })} /></label>
                </div>
              </details>
            </div>
          </details>
        </article>)}
      </div>
      <div className="probe-rule-trial">
        <div className="probe-section-head"><h3>Test rules</h3></div>
        <div className="probe-trial-inputs">
          <label>Expected errors<textarea value={trialPositive} onChange={(event) => setTrialPositive(event.target.value)} placeholder="Paste redacted error log lines" /></label>
          <label>Expected non-errors<textarea value={trialNegative} onChange={(event) => setTrialNegative(event.target.value)} placeholder="Paste redacted normal or ignored log lines" /></label>
        </div>
        <div className="probe-trial-actions">
          <button className="secondary-button" type="button" disabled={trialPending || (!trialPositive.trim() && !trialNegative.trim())} onClick={() => void runTrial()}>
            {trialPending ? <LoaderCircle className="spin" size={15} /> : <FlaskConical size={15} />}Run test
          </button>
          {trialError != null && <p className="credential-field-error" role="alert">{messageFromError(trialError)}</p>}
        </div>
        {trialResult && <div className="probe-trial-results" role="status">
          <p>{trialResult.positiveCount === 0 || trialResult.negativeCount === 0 ? "Only one sample category provided; coverage is incomplete." : trialSummary?.missing.length === 0 && trialSummary.unexpected.length === 0 ? "Matches these samples" : "Needs review"}</p>
          <p>Errors: {trialSummary?.covered}/{trialResult.positiveCount} matched · Non-errors: {trialSummary?.unexpected.length}/{trialResult.negativeCount} matched</p>
          {trialSummary && trialSummary.missing.length > 0 && <p>Missed error lines: {trialSummary.missing.join(", ")}</p>}
          {trialSummary && trialSummary.unexpected.length > 0 && <p>Unexpected match lines: {trialSummary.unexpected.join(", ")}</p>}
          {trialResult.rules.map((result) => <div className="probe-trial-rule" key={result.ruleId}>
            <strong>{customRules.find((rule) => rule.id === result.ruleId)?.name ?? result.ruleId}</strong>
            <span>{result.positiveMatches.length} errors · {result.negativeMatches.length} non-errors matched</span>
            {result.positiveExcluded.length > 0 && <small>Excluded error lines: {result.positiveExcluded.join(", ")}</small>}
          </div>)}
        </div>}
      </div>
      <details className="probe-global-settings">
        <summary>Incident grouping</summary>
        <label>Grouping window<select value={groupingWindowSeconds} onChange={(event) => setGroupingWindowSeconds(Number(event.target.value))}>
          {!COMMON_WINDOWS.includes(groupingWindowSeconds) && <option value={groupingWindowSeconds}>{durationLabel(groupingWindowSeconds)}</option>}
          {COMMON_WINDOWS.map((seconds) => <option key={seconds} value={seconds}>{durationLabel(seconds)}</option>)}
        </select></label>
      </details>
      <div className="probe-management" data-state={probeChecking ? "starting" : (probeTimedOut || (logProbeStatus?.state === "active" && logProbeStatus.message !== "monitoring")) ? "unknown" : logProbeStatus?.state ?? "unknown"}>
        <div className="probe-management-row">
          <div className="probe-status">
            <span className="probe-status-icon"><ServerCog size={16} /></span>
            <div>
              <strong>{probeChecking ? "Starting monitoring" : probeTimedOut ? "Monitoring needs attention" : probeStatusLabel(logProbeStatus)}</strong>
              {probeChecking ? <small>Checking the service status...</small> : probeTimedOut ? <small>Could not confirm monitoring. Refresh the status or check the remote service.</small> : logProbeStatus?.message && <small>{logProbeStatus.message}</small>}
            </div>
          </div>
          <div className="probe-management-actions">
            <button className="secondary-button" type="button" disabled={!canManageLogProbe || probeBusy || probeChecking} onClick={onInstallLogProbe}>{installingLogProbe || probeChecking ? <LoaderCircle className="spin" size={15} /> : <ServerCog size={15} />}{logProbeStatus?.state === "active" ? "Apply changes" : "Enable monitoring"}</button>
            <button className="icon-button" type="button" title="Refresh monitoring status" aria-label="Refresh monitoring status" disabled={!canManageLogProbe || probeBusy} onClick={onRefreshLogProbe}>{refreshingLogProbe ? <LoaderCircle className="spin" size={15} /> : <RefreshCw size={15} />}</button>
            <button className="icon-button" type="button" title="Disable monitoring" aria-label="Disable monitoring" disabled={!canManageLogProbe || probeBusy || logProbeStatus?.state === "not_installed"} onClick={onUninstallLogProbe}>{uninstallingLogProbe ? <LoaderCircle className="spin" size={15} /> : <Unplug size={15} />}</button>
          </div>
        </div>
        {!canManageLogProbe && <p className="field-hint">Save the SSH source and rules before enabling monitoring.</p>}
        {probeError !== undefined && probeError !== null && <p className="credential-field-error" role="alert">{messageFromError(probeError)}</p>}
      </div>
    </>}
    <div className="setup-section-actions">
      <button className="primary-button" type="button" disabled={saving || !canSave} onClick={onSave}>{saving ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}{triggerKind === "custom_rule" ? "Save rules" : "Save trigger"}</button>
      {saveError !== undefined && saveError !== null && <p className="credential-field-error" role="alert">{messageFromError(saveError)}</p>}
    </div>
  </section>;
}
