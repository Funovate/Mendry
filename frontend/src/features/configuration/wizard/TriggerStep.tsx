import { Activity, Check, Copy, LoaderCircle } from "lucide-react";
import { useState } from "react";
import { messageFromError, type TriggerKind } from "../../../api";
import type { WebhookProvider } from "../configuration";

export function TriggerStep({
  triggerKind, setTriggerKind,
  webhookProvider, setWebhookProvider,
  awsTopicArn, setAwsTopicArn,
  groupingWindowSeconds, setGroupingWindowSeconds, matchExpression, setMatchExpression,
  inboundUrl, onGenerateInboundUrl, generatingInboundUrl = false, generateInboundUrlError,
  canGenerateInboundUrl = false,
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
  matchExpression: string;
  setMatchExpression: (value: string) => void;
  inboundUrl?: string | null;
  onGenerateInboundUrl?: () => Promise<string>;
  generatingInboundUrl?: boolean;
  generateInboundUrlError?: unknown;
  canGenerateInboundUrl?: boolean;
  onSave: () => void;
  saving?: boolean;
  canSave?: boolean;
  saveError?: unknown;
}) {
  const [copied, setCopied] = useState(false);
  const copyUrl = async () => {
    if (!inboundUrl) return;
    await navigator.clipboard.writeText(inboundUrl);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };
  return <section>
    <div className="setup-card-title"><Activity size={20} /><div><h2>Trigger</h2><p>Signed webhooks or custom rules decide how normalized events enter incident grouping.</p></div></div>
    <div className="source-form">
      <label>Trigger type<select aria-label="Trigger type" value={triggerKind} onChange={(event) => setTriggerKind(event.target.value as TriggerKind)}>
        <option value="custom_rule">Custom rule</option>
        <option value="signed_webhook">Signed webhook</option>
      </select></label>
    </div>
    {triggerKind === "signed_webhook" ? <>
      <label>Webhook provider<select aria-label="Webhook provider" value={webhookProvider} onChange={(event) => setWebhookProvider(event.target.value as WebhookProvider)}>
        <option value="generic">Generic webhook</option>
        <option value="tencent_cls">Tencent Cloud CLS alert</option>
        <option value="aws_cloudwatch">AWS CloudWatch Alarm via SNS</option>
      </select></label>
      {webhookProvider === "aws_cloudwatch" && <label>SNS Topic ARN<input aria-label="SNS Topic ARN" value={awsTopicArn} onChange={(event) => setAwsTopicArn(event.target.value)} placeholder="arn:aws:sns:us-east-1:123456789012:mendry-alarms" /></label>}
      <div className="inbound-url-field">
      <label>Inbound webhook URL<input aria-label="Inbound webhook URL" value={inboundUrl ?? ""} readOnly placeholder="Generate an inbound URL after the project is saved." /></label>
      <div className="inbound-url-actions">
        <button className="secondary-button" type="button" disabled={!inboundUrl} onClick={() => void copyUrl()}>
          {copied ? <Check size={16} /> : <Copy size={16} />}{copied ? "Copied" : "Copy URL"}
        </button>
        {onGenerateInboundUrl && <button className="secondary-button" type="button" disabled={generatingInboundUrl || !canGenerateInboundUrl} onClick={() => void onGenerateInboundUrl()}>
          {generatingInboundUrl ? <LoaderCircle className="spin" size={16} /> : null}{inboundUrl ? "Regenerate URL" : "Generate URL"}
        </button>}
      </div>
      {!canGenerateInboundUrl && <p className="inbound-url-hint">Save the signed webhook configuration first. The first save creates the inbound URL.</p>}
      {generateInboundUrlError instanceof Error && <p role="alert">{generateInboundUrlError.message}</p>}
      </div>
    </> : <>
      <label>Grouping window seconds<input aria-label="Grouping window seconds" type="number" min="1" max="86400" value={groupingWindowSeconds} onChange={(event) => setGroupingWindowSeconds(Number(event.target.value))} /></label>
      <label>Match expression<textarea aria-label="Match expression" value={matchExpression} onChange={(event) => setMatchExpression(event.target.value)} /></label>
    </>}
    <div className="setup-section-actions">
      <button className="primary-button" type="button" disabled={saving || !canSave} onClick={onSave}>
        {saving ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Save trigger
      </button>
      {saveError !== undefined && saveError !== null && <p className="credential-field-error" role="alert">{messageFromError(saveError)}</p>}
    </div>
  </section>;
}
