import { BrainCircuit, Check, LoaderCircle } from "lucide-react";
import type { ProjectSecret } from "../../../api";
import { messageFromError } from "../../../api";
import { CredentialField } from "./CredentialField";

const LLM_CREDENTIAL_KINDS: { value: ProjectSecret["kind"]; label: string }[] = [
  { value: "http_bearer", label: "HTTP bearer token" },
];

export function LLMStep({
  baseUrl, setBaseUrl, credentialId, setCredentialId, model, setModel,
  models, onLoadModels, loadingModels, loadModelsError,
  onTestChat, testingChat, testChatError, chatReady,
  onSave, saving = false, canSave = false, saveError,
  knownSecrets, createCredential, creatingCredential, createCredentialError,
  updateCredential, updatingCredential = false, updateCredentialError,
}: {
  baseUrl: string;
  setBaseUrl: (value: string) => void;
  credentialId: string;
  setCredentialId: (value: string) => void;
  model: string;
  setModel: (value: string) => void;
  models: string[];
  onLoadModels: () => void;
  loadingModels: boolean;
  loadModelsError?: unknown;
  onTestChat: () => void;
  testingChat: boolean;
  testChatError?: unknown;
  chatReady: boolean;
  onSave: () => void;
  saving?: boolean;
  canSave?: boolean;
  saveError?: unknown;
  knownSecrets: ProjectSecret[];
  createCredential: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => Promise<ProjectSecret>;
  creatingCredential: boolean;
  createCredentialError?: unknown;
  updateCredential: (input: { secretId: string; name: string; value?: string }) => Promise<ProjectSecret>;
  updatingCredential?: boolean;
  updateCredentialError?: unknown;
}) {
  const bearerSecrets = knownSecrets.filter((secret) => secret.kind === "http_bearer");
  const options = model && !models.includes(model) ? [model, ...models] : models;

  return <section>
    <div className="setup-card-title"><BrainCircuit size={20} /><div><h2>LLM provider</h2><p>OpenAI-compatible Chat Completions need a base URL, encrypted API key, and a model selected from the provider list.</p></div></div>
    <label>Provider<input aria-label="LLM provider" value="openai" readOnly /></label>
    <label>Base URL<input aria-label="LLM base URL" value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="https://api.openai.com" /></label>
    <CredentialField
      label="API key credential" value={credentialId} onChange={setCredentialId} secrets={bearerSecrets}
      required createLabelPrefix="LLM" allowedCreateKinds={LLM_CREDENTIAL_KINDS} onCreate={createCredential}
      creating={creatingCredential} createError={createCredentialError}
      onUpdate={updateCredential} updating={updatingCredential} updateError={updateCredentialError}
    />
    <div className="source-form">
      <label>Model<select aria-label="LLM model" value={model} required onChange={(event) => setModel(event.target.value)}>
        <option value="">{options.length ? "Select a model" : "Load models first"}</option>
        {options.map((id) => <option value={id} key={id}>{id}</option>)}
      </select></label>
      <button className="secondary-button" type="button" disabled={loadingModels || !credentialId || !baseUrl.trim()} onClick={onLoadModels}>
        {loadingModels ? <LoaderCircle className="spin" size={16} /> : <BrainCircuit size={16} />}Load models
      </button>
    </div>
    <div className="source-form">
      <button className="secondary-button" type="button" disabled={testingChat || !credentialId || !baseUrl.trim() || !model.trim()} onClick={onTestChat}>
        {testingChat ? <LoaderCircle className="spin" size={16} /> : <BrainCircuit size={16} />}Test with hi
      </button>
      {chatReady && <p className="setup-footer-saved" role="status">Chat probe succeeded.</p>}
    </div>
    {loadModelsError !== undefined && loadModelsError !== null && <p className="credential-field-error">{messageFromError(loadModelsError)}</p>}
    {testChatError !== undefined && testChatError !== null && <p className="credential-field-error">{messageFromError(testChatError)}</p>}
    <div className="setup-section-actions">
      <button className="primary-button" type="button" disabled={saving || !canSave} onClick={onSave}>
        {saving ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Save LLM provider
      </button>
      {saveError !== undefined && saveError !== null && <p className="credential-field-error" role="alert">{messageFromError(saveError)}</p>}
    </div>
    <p className="choice-note">Load models checks that the API key can list models. Test with hi sends a bounded Chat Completions request so an unusable model cannot be saved.</p>
  </section>;
}
