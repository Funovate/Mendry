import { Check, GitBranch, LoaderCircle, LockKeyhole, Pencil, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { messageFromError, type ProjectConfiguration, type ProjectSecret } from "../../../api";
import {
  compatibleGitSecretId,
  composeGitSecretReplacement,
  composeHttpsGitCredentialValue,
  composeSshPrivateKeyValue,
  filterGitSecrets,
  inspectSshPrivateKeyDraft,
  sshPrivateKeyInspectionMessage,
} from "../configuration";
import { SshPrivateKeyDraftField } from "./SshPrivateKeyDraftField";

export function RepositoryStep({
  remoteUrl, setRemoteUrl, scmProvider, setScmProvider, transport, setTransport,
  repositorySecretId, setRepositorySecretId, productionBranch, deployedCommit, branches,
  onSelectBranch, onReadRemote, readingRemote, readRemoteError,
  onSave, saving = false, canSave = false, saveError,
  knownSecrets, createCredential, creatingCredential, createCredentialError,
  updateCredential, updatingCredential = false, updateCredentialError,
}: {
  remoteUrl: string;
  setRemoteUrl: (value: string) => void;
  scmProvider: ProjectConfiguration["repository"]["scmProvider"];
  setScmProvider: (value: ProjectConfiguration["repository"]["scmProvider"]) => void;
  transport: ProjectConfiguration["repository"]["transport"];
  setTransport: (value: ProjectConfiguration["repository"]["transport"]) => void;
  repositorySecretId: string;
  setRepositorySecretId: (value: string) => void;
  productionBranch: string;
  deployedCommit: string;
  branches: { name: string; commit: string }[];
  onSelectBranch: (name: string, commit: string) => void;
  onReadRemote: () => void;
  readingRemote: boolean;
  readRemoteError?: unknown;
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
  const gitSecrets = filterGitSecrets(knownSecrets, transport);
  const changeTransport = (next: ProjectConfiguration["repository"]["transport"]) => {
    setTransport(next);
    setRepositorySecretId(compatibleGitSecretId(repositorySecretId, knownSecrets, next));
  };

  return <section>
    <div className="setup-card-title"><GitBranch size={20} /><div><h2>Git repository</h2><p>The remote and immutable deployed commit define the production code baseline.</p></div></div>
    <label>Git remote URL<input aria-label="Git remote URL" value={remoteUrl} onChange={(event) => setRemoteUrl(event.target.value)} /></label>
    <div className="source-form">
      <label>SCM provider<select aria-label="SCM provider" value={scmProvider} onChange={(event) => setScmProvider(event.target.value as ProjectConfiguration["repository"]["scmProvider"])}>
        <option value="generic">Generic Git</option>
        <option value="github">GitHub</option>
        <option value="gitlab">GitLab</option>
        <option value="yunxiao">Yunxiao</option>
        <option value="gitee">Gitee</option>
      </select></label>
      <label>Transport<select aria-label="Git transport" value={transport} onChange={(event) => changeTransport(event.target.value as "https" | "ssh")}>
        <option value="https">HTTPS</option>
        <option value="ssh">SSH</option>
      </select></label>
    </div>
    <GitCredentialField
      value={repositorySecretId} onChange={setRepositorySecretId} secrets={gitSecrets} transport={transport}
      onCreate={createCredential} creating={creatingCredential} createError={createCredentialError}
      onUpdate={updateCredential} updating={updatingCredential} updateError={updateCredentialError}
    />
    <div className="git-baseline">
      <div className="source-form">
        <label>Production branch<select aria-label="Production branch" value={productionBranch} disabled={branches.length === 0} onChange={(event) => {
          const selected = branches.find((branch) => branch.name === event.target.value);
          if (selected) onSelectBranch(selected.name, selected.commit);
        }}>
          {branches.length === 0 && <option value="">{productionBranch || "Read from remote"}</option>}
          {branches.map((branch) => <option value={branch.name} key={branch.name}>{branch.name}</option>)}
        </select></label>
        <label>Deployed commit<input aria-label="Deployed commit" value={deployedCommit} readOnly /></label>
      </div>
      <button className="secondary-button" type="button" disabled={readingRemote || !repositorySecretId || !remoteUrl.trim()} onClick={onReadRemote}>
        {readingRemote ? <LoaderCircle className="spin" size={16} /> : <GitBranch size={16} />}Read from remote
      </button>
      {readRemoteError !== undefined && readRemoteError !== null && <p className="credential-field-error">{messageFromError(readRemoteError)}</p>}
    </div>
    <div className="setup-section-actions">
      <button className="primary-button" type="button" disabled={saving || !canSave} onClick={onSave}>
        {saving ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Save Git repository
      </button>
      {saveError !== undefined && saveError !== null && <p className="credential-field-error" role="alert">{messageFromError(saveError)}</p>}
    </div>
  </section>;
}

function GitCredentialField({
  value, onChange, secrets, transport, onCreate, creating, createError, onUpdate, updating = false, updateError,
}: {
  value: string;
  onChange: (value: string) => void;
  secrets: ProjectSecret[];
  transport: ProjectConfiguration["repository"]["transport"];
  onCreate: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => Promise<ProjectSecret>;
  creating: boolean;
  createError?: unknown;
  onUpdate: (input: { secretId: string; name: string; value?: string }) => Promise<ProjectSecret>;
  updating?: boolean;
  updateError?: unknown;
}) {
  const [mode, setMode] = useState<"idle" | "create" | "edit">("idle");
  const [name, setName] = useState("");
  const [username, setUsername] = useState("");
  const [secret, setSecret] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [sshPassword, setSshPassword] = useState("");
  const [editName, setEditName] = useState("");
  const [keyError, setKeyError] = useState<string | null>(null);
  const selected = secrets.find((item) => item.id === value);

  const resetDraft = () => {
    setName("");
    setUsername("");
    setSecret("");
    setPrivateKey("");
    setPassphrase("");
    setSshPassword("");
    setEditName("");
    setKeyError(null);
  };
  const closeForms = () => {
    resetDraft();
    setMode("idle");
  };
  useEffect(() => {
    setMode("idle");
    setName("");
    setUsername("");
    setSecret("");
    setPrivateKey("");
    setPassphrase("");
    setSshPassword("");
    setEditName("");
    setKeyError(null);
  }, [transport]);
  const changeSelection = (next: string) => {
    closeForms();
    onChange(next);
  };
  const toggleCreate = () => {
    if (mode === "create") {
      closeForms();
      return;
    }
    resetDraft();
    setMode("create");
  };
  const toggleEdit = () => {
    if (mode === "edit" || !selected) {
      closeForms();
      return;
    }
    resetDraft();
    setEditName(selected.name);
    setMode("edit");
  };

  const createKind: ProjectSecret["kind"] = transport === "https" ? "git_credential" : "ssh_private_key";
  const editKind = selected?.kind ?? createKind;
  const composedCreateValue = transport === "https"
    ? composeHttpsGitCredentialValue(username, secret)
    : composeSshPrivateKeyValue(privateKey, passphrase);
  const replacement = composeGitSecretReplacement(editKind, { username, secret, privateKey, passphrase, sshPassword });
  const canStore = name.trim().length > 0 && composedCreateValue.length > 0;
  const canSaveEdit = editName.trim().length > 0 && replacement.complete;

  const submitCreate = async () => {
    if (createKind === "ssh_private_key") {
      const inspection = inspectSshPrivateKeyDraft(composedCreateValue);
      if (!inspection.ok) {
        setKeyError(sshPrivateKeyInspectionMessage(inspection.reason));
        return;
      }
    }
    const created = await onCreate({
      name: name.trim(),
      kind: createKind,
      value: composedCreateValue,
    });
    onChange(created.id);
    closeForms();
  };
  const submitEdit = async () => {
    if (!selected) return;
    if (selected.kind === "ssh_private_key" && replacement.value !== undefined) {
      const inspection = inspectSshPrivateKeyDraft(replacement.value);
      if (!inspection.ok) {
        setKeyError(sshPrivateKeyInspectionMessage(inspection.reason));
        return;
      }
    }
    await onUpdate({
      secretId: selected.id,
      name: editName.trim(),
      ...(replacement.value === undefined ? {} : { value: replacement.value }),
    });
    closeForms();
  };

  return <div className="credential-field">
    <label>Git credential reference<select aria-label="Git credential reference" value={value} onChange={(event) => changeSelection(event.target.value)}>
      <option value="">No credential</option>
      {secrets.map((item) => <option value={item.id} key={item.id}>{item.name} · {item.kind}</option>)}
    </select></label>
    <div className="credential-field-actions">
      <button type="button" className="credential-field-toggle" onClick={toggleCreate}>
        <Plus size={13} />{mode === "create" ? "Cancel new credential" : "New credential"}
      </button>
      {selected && <button type="button" className="credential-field-toggle" onClick={toggleEdit}>
        <Pencil size={13} />{mode === "edit" ? "Cancel edit credential" : "Edit credential"}
      </button>}
    </div>
    {mode === "create" && <div className="credential-inline-form">
      <label>Git credential name<input aria-label="Git credential name" value={name} onChange={(event) => setName(event.target.value)} /></label>
      <GitSecretInputs kind={createKind} username={username} setUsername={setUsername} secret={secret} setSecret={setSecret} privateKey={privateKey} setPrivateKey={(next) => { setKeyError(null); setPrivateKey(next); }} passphrase={passphrase} setPassphrase={setPassphrase} sshPassword={sshPassword} setSshPassword={setSshPassword} />
      <button className="secondary-button" type="button" disabled={creating || !canStore} onClick={() => void submitCreate()}>
        {creating ? <LoaderCircle className="spin" size={16} /> : <LockKeyhole size={16} />}Store git credential
      </button>
      {keyError !== null && <p className="credential-field-error">{keyError}</p>}
      {createError !== undefined && createError !== null && <p className="credential-field-error">{messageFromError(createError)}</p>}
    </div>}
    {mode === "edit" && selected && <div className="credential-inline-form">
      <div className="source-form">
        <label>Git credential name<input aria-label="Git credential name" value={editName} onChange={(event) => setEditName(event.target.value)} /></label>
        <label>Git credential type<input aria-label="Git credential type" value={selected.kind} readOnly /></label>
      </div>
      <GitSecretInputs kind={selected.kind} username={username} setUsername={setUsername} secret={secret} setSecret={setSecret} privateKey={privateKey} setPrivateKey={(next) => { setKeyError(null); setPrivateKey(next); }} passphrase={passphrase} setPassphrase={setPassphrase} sshPassword={sshPassword} setSshPassword={setSshPassword} replacement />
      <button className="secondary-button" type="button" disabled={updating || !canSaveEdit} onClick={() => void submitEdit()}>
        {updating ? <LoaderCircle className="spin" size={16} /> : <Pencil size={16} />}Save git credential
      </button>
      {keyError !== null && <p className="credential-field-error">{keyError}</p>}
      {updateError !== undefined && updateError !== null && <p className="credential-field-error">{messageFromError(updateError)}</p>}
    </div>}
  </div>;
}

function GitSecretInputs({
  kind, username, setUsername, secret, setSecret, privateKey, setPrivateKey, passphrase, setPassphrase, sshPassword, setSshPassword, replacement = false,
}: {
  kind: ProjectSecret["kind"];
  username: string;
  setUsername: (value: string) => void;
  secret: string;
  setSecret: (value: string) => void;
  privateKey: string;
  setPrivateKey: (value: string) => void;
  passphrase: string;
  setPassphrase: (value: string) => void;
  sshPassword: string;
  setSshPassword: (value: string) => void;
  replacement?: boolean;
}) {
  if (kind === "git_credential") {
    return <>
      <label>Git username<input aria-label="Git username" value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="off" /></label>
      <label className="secret-input"><span>{replacement ? "Replacement Git HTTP password or token" : "Git HTTP password or token"}</span>
        <input aria-label={replacement ? "Replacement Git HTTP password or token" : "Git HTTP password or token"} type="password" autoComplete="off" value={secret} onChange={(event) => setSecret(event.target.value)} placeholder={replacement ? "Leave empty to keep the current secret" : undefined} />
      </label>
    </>;
  }
  if (kind === "ssh_password") {
    return <>
      <label>Git username<input aria-label="Git username" value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="off" /></label>
      <label className="secret-input"><span>{replacement ? "Replacement SSH password" : "SSH password"}</span>
        <input aria-label={replacement ? "Replacement SSH password" : "SSH password"} type="password" autoComplete="off" value={sshPassword} onChange={(event) => setSshPassword(event.target.value)} placeholder={replacement ? "Leave empty to keep the current secret" : undefined} />
      </label>
    </>;
  }
  return <>
    <SshPrivateKeyDraftField
      value={privateKey}
      onChange={setPrivateKey}
      textareaLabel={replacement ? "Replacement SSH private key" : "SSH private key"}
      fileLabel={replacement ? "Replacement SSH private key file" : "SSH private key file"}
      placeholder={replacement ? "Leave empty to keep the current secret" : "Paste private key; stored encrypted and never displayed again"}
      passphrase={passphrase}
      includePassphraseInSize
    />
    <label className="secret-input"><span>SSH passphrase (optional)</span>
      <input aria-label="SSH passphrase" type="password" autoComplete="off" value={passphrase} onChange={(event) => setPassphrase(event.target.value)} />
    </label>
  </>;
}
