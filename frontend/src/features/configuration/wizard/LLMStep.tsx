import { BrainCircuit, Check, LoaderCircle } from "lucide-react";
import type { LLMAPIMode, LLMProviderName, ProjectSecret } from "../../../api";
import { messageFromError } from "../../../api";
import { CredentialField } from "./CredentialField";

const LLM_CREDENTIAL_KINDS: { value: ProjectSecret["kind"]; label: string }[] = [
  { value: "http_bearer", label: "HTTP bearer token" },
];

export const LLM_DEFAULT_BASE_URL: Record<LLMProviderName, string> = {
  openai: "https://api.openai.com",
  anthropic: "https://api.anthropic.com",
};

export const LLM_DEFAULT_API_MODE: Record<LLMProviderName, LLMAPIMode> = {
  openai: "chat_completions",
  anthropic: "messages",
};

export function LLMStep({
  provider, setProvider, baseUrl, setBaseUrl, credentialId, setCredentialId, model, setModel, apiMode, setAPIMode,
  models, onLoadModels, loadingModels, loadModelsError,
  onTestChat, testingChat, testChatError, chatReady,
  onSave, saving = false, canSave = false, saveError,
  knownSecrets, createCredential, creatingCredential, createCredentialError,
  updateCredential, updatingCredential = false, updateCredentialError,
}: {
  provider: LLMProviderName;
  setProvider: (value: LLMProviderName) => void;
  baseUrl: string;
  setBaseUrl: (value: string) => void;
  credentialId: string;
  setCredentialId: (value: string) => void;
  model: string;
  setModel: (value: string) => void;
  apiMode: LLMAPIMode;
  setAPIMode: (value: LLMAPIMode) => void;
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
    <div className="setup-card-title">
      <div className="setup-card-icon">
        <BrainCircuit size={18} />
      </div>
      <h2>LLM provider</h2>
    </div>
    <label>
      Provider
      <div className="segmented-control" role="group" aria-label="LLM provider">
        <button type="button" className={provider === "openai" ? "active" : ""} aria-pressed={provider === "openai"} onClick={() => setProvider("openai")}>OpenAI compatible</button>
        <button type="button" className={provider === "anthropic" ? "active" : ""} aria-pressed={provider === "anthropic"} onClick={() => setProvider("anthropic")}>Anthropic</button>
      </div>
    </label>
    {provider === "openai" && <label>
      API
      <div className="segmented-control" role="group" aria-label="LLM API">
        <button type="button" className={apiMode === "chat_completions" ? "active" : ""} aria-pressed={apiMode === "chat_completions"} onClick={() => setAPIMode("chat_completions")}>Chat Completions</button>
        <button type="button" className={apiMode === "responses" ? "active" : ""} aria-pressed={apiMode === "responses"} onClick={() => setAPIMode("responses")}>Responses</button>
      </div>
    </label>}
    <label>Base URL<input aria-label="LLM base URL" value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder={LLM_DEFAULT_BASE_URL[provider]} /></label>
    <CredentialField
      label="API key credential" value={credentialId} onChange={setCredentialId} secrets={bearerSecrets}
      required createLabelPrefix="LLM" allowedCreateKinds={LLM_CREDENTIAL_KINDS} onCreate={createCredential}
      creating={creatingCredential} createError={createCredentialError}
      onUpdate={updateCredential} updating={updatingCredential} updateError={updateCredentialError}
    />
    <label>
      Model
      <div className="form-input-action-row">
        <select aria-label="LLM model" value={model} required onChange={(event) => setModel(event.target.value)}>
          <option value="">{options.length ? "Select a model" : "Load models first"}</option>
          {options.map((id) => <option value={id} key={id}>{id}</option>)}
        </select>
        <button className="secondary-button" type="button" disabled={loadingModels || !credentialId || !baseUrl.trim()} onClick={onLoadModels}>
          {loadingModels ? <LoaderCircle className="spin" size={15} /> : <BrainCircuit size={15} />}Load models
        </button>
        <button className="secondary-button" type="button" disabled={testingChat || !credentialId || !baseUrl.trim() || !model.trim()} onClick={onTestChat}>
          {testingChat ? <LoaderCircle className="spin" size={15} /> : <BrainCircuit size={15} />}Test with hi
        </button>
      </div>
    </label>
    {chatReady && <p className="setup-footer-saved" role="status">Chat probe succeeded.</p>}
    {loadModelsError !== undefined && loadModelsError !== null && <p className="credential-field-error">{messageFromError(loadModelsError)}</p>}
    {testChatError !== undefined && testChatError !== null && <p className="credential-field-error">{messageFromError(testChatError)}</p>}
    <div className="setup-section-actions">
      <button className="primary-button" type="button" disabled={saving || !canSave} onClick={onSave}>
        {saving ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Save LLM provider
      </button>
      {saveError !== undefined && saveError !== null && <p className="credential-field-error" role="alert">{messageFromError(saveError)}</p>}
    </div>
    <p className="choice-note">Load models checks that the API key can list models. Test with hi sends a bounded request through the selected API so an unusable model cannot be saved.</p>
  </section>;
}
