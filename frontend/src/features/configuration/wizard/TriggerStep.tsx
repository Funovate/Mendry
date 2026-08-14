import { Activity } from "lucide-react";
import type { ProjectSecret, TriggerKind } from "../../../api";
import { CredentialField } from "./CredentialField";

const WEBHOOK_CREDENTIAL_KINDS: { value: ProjectSecret["kind"]; label: string }[] = [
  { value: "webhook_hmac", label: "Webhook HMAC" },
];

export function TriggerStep({
  triggerName, setTriggerName, triggerKind, setTriggerKind, signingSecretId, setSigningSecretId,
  eventTypes, setEventTypes, deduplicationKey, setDeduplicationKey, groupingWindowSeconds, setGroupingWindowSeconds,
  matchExpression, setMatchExpression, knownSecrets, createCredential, creatingCredential, createCredentialError,
  updateCredential, updatingCredential = false, updateCredentialError,
}: {
  triggerName: string;
  setTriggerName: (value: string) => void;
  triggerKind: TriggerKind;
  setTriggerKind: (value: TriggerKind) => void;
  signingSecretId: string;
  setSigningSecretId: (value: string) => void;
  eventTypes: string;
  setEventTypes: (value: string) => void;
  deduplicationKey: string;
  setDeduplicationKey: (value: string) => void;
  groupingWindowSeconds: number;
  setGroupingWindowSeconds: (value: number) => void;
  matchExpression: string;
  setMatchExpression: (value: string) => void;
  knownSecrets: ProjectSecret[];
  createCredential: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => Promise<ProjectSecret>;
  creatingCredential: boolean;
  createCredentialError?: unknown;
  updateCredential: (input: { secretId: string; name: string; value?: string }) => Promise<ProjectSecret>;
  updatingCredential?: boolean;
  updateCredentialError?: unknown;
}) {
  return <section>
    <div className="setup-card-title"><Activity size={20} /><div><h2>Trigger</h2><p>Signed webhooks or custom rules decide how normalized events enter incident grouping.</p></div></div>
    <div className="source-form">
      <label>Trigger name<input aria-label="Trigger name" value={triggerName} onChange={(event) => setTriggerName(event.target.value)} /></label>
      <label>Trigger type<select aria-label="Trigger type" value={triggerKind} onChange={(event) => setTriggerKind(event.target.value as TriggerKind)}>
        <option value="custom_rule">Custom rule</option>
        <option value="signed_webhook">Signed webhook</option>
      </select></label>
    </div>
    {triggerKind === "signed_webhook" ? <>
      <CredentialField
        label="Webhook signing credential" value={signingSecretId} onChange={setSigningSecretId}
        secrets={knownSecrets.filter((secret) => secret.kind === "webhook_hmac")} required
        createLabelPrefix="Webhook" allowedCreateKinds={WEBHOOK_CREDENTIAL_KINDS} onCreate={createCredential}
        creating={creatingCredential} createError={createCredentialError}
        onUpdate={updateCredential} updating={updatingCredential} updateError={updateCredentialError}
      />
      <label>Event types<input aria-label="Webhook event types" value={eventTypes} onChange={(event) => setEventTypes(event.target.value)} /></label>
      <label>Deduplication key<input aria-label="Deduplication key" value={deduplicationKey} onChange={(event) => setDeduplicationKey(event.target.value)} /></label>
    </> : <>
      <label>Grouping window seconds<input aria-label="Grouping window seconds" type="number" min="1" max="86400" value={groupingWindowSeconds} onChange={(event) => setGroupingWindowSeconds(Number(event.target.value))} /></label>
      <label>Match expression<textarea aria-label="Match expression" value={matchExpression} onChange={(event) => setMatchExpression(event.target.value)} /></label>
    </>}
  </section>;
}
