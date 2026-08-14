import { LoaderCircle, LockKeyhole, Pencil, Plus } from "lucide-react";
import { useState } from "react";
import { messageFromError, type ProjectSecret } from "../../../api";

export function CredentialField({
  label, value, onChange, secrets, required = false,
  createLabelPrefix, allowedCreateKinds, onCreate, creating, createError,
  onUpdate, updating = false, updateError,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  secrets: ProjectSecret[];
  required?: boolean;
  createLabelPrefix: string;
  allowedCreateKinds: { value: ProjectSecret["kind"]; label: string }[];
  onCreate: (input: { name: string; kind: ProjectSecret["kind"]; value: string }) => Promise<ProjectSecret>;
  creating: boolean;
  createError?: unknown;
  onUpdate: (input: { secretId: string; name: string; value?: string }) => Promise<ProjectSecret>;
  updating?: boolean;
  updateError?: unknown;
}) {
  const [mode, setMode] = useState<"idle" | "create" | "edit">("idle");
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ProjectSecret["kind"]>(allowedCreateKinds[0]?.value ?? "git_credential");
  const [draftValue, setDraftValue] = useState("");
  const [editName, setEditName] = useState("");
  const [editValue, setEditValue] = useState("");
  const selected = secrets.find((secret) => secret.id === value);

  const resetCreate = () => {
    setName("");
    setDraftValue("");
    setKind(allowedCreateKinds[0]?.value ?? "git_credential");
  };
  const resetEdit = () => {
    setEditName("");
    setEditValue("");
  };
  const closeForms = () => {
    resetCreate();
    resetEdit();
    setMode("idle");
  };
  const changeSelection = (next: string) => {
    closeForms();
    onChange(next);
  };
  const toggleCreate = () => {
    if (mode === "create") {
      closeForms();
      return;
    }
    resetEdit();
    resetCreate();
    setMode("create");
  };
  const toggleEdit = () => {
    if (mode === "edit" || !selected) {
      closeForms();
      return;
    }
    resetCreate();
    setEditName(selected.name);
    setEditValue("");
    setMode("edit");
  };
  const submitCreate = async () => {
    const created = await onCreate({ name: name.trim(), kind, value: draftValue });
    onChange(created.id);
    closeForms();
  };
  const submitEdit = async () => {
    if (!selected) return;
    const replacement = editValue.trim();
    await onUpdate({
      secretId: selected.id,
      name: editName.trim(),
      ...(replacement ? { value: replacement } : {}),
    });
    closeForms();
  };

  return <div className="credential-field">
    <label>{label}<select aria-label={label} value={value} required={required} onChange={(event) => changeSelection(event.target.value)}>
      <option value="">{required ? "Select a credential" : "No credential"}</option>
      {secrets.map((secret) => <option value={secret.id} key={secret.id}>{secret.name} · {secret.kind}</option>)}
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
      <div className="source-form">
        <label>{createLabelPrefix} credential name<input aria-label={`${createLabelPrefix} credential name`} value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label>{createLabelPrefix} credential type<select aria-label={`${createLabelPrefix} credential type`} value={kind} onChange={(event) => setKind(event.target.value as ProjectSecret["kind"])}>
          {allowedCreateKinds.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
        </select></label>
      </div>
      <label>{createLabelPrefix} credential value<input aria-label={`${createLabelPrefix} credential value`} type="password" autoComplete="off" value={draftValue} onChange={(event) => setDraftValue(event.target.value)} /></label>
      <button className="secondary-button" type="button" disabled={creating || !name.trim() || !draftValue} onClick={() => void submitCreate()}>
        {creating ? <LoaderCircle className="spin" size={16} /> : <LockKeyhole size={16} />}Store {createLabelPrefix.toLowerCase()} credential
      </button>
      {createError !== undefined && createError !== null && <p className="credential-field-error">{messageFromError(createError)}</p>}
    </div>}
    {mode === "edit" && selected && <div className="credential-inline-form">
      <div className="source-form">
        <label>{createLabelPrefix} credential name<input aria-label={`${createLabelPrefix} credential name`} value={editName} onChange={(event) => setEditName(event.target.value)} /></label>
        <label>{createLabelPrefix} credential type<input aria-label={`${createLabelPrefix} credential type`} value={selected.kind} readOnly /></label>
      </div>
      <label>{createLabelPrefix} replacement value<input aria-label={`${createLabelPrefix} replacement value`} type="password" autoComplete="off" value={editValue} onChange={(event) => setEditValue(event.target.value)} placeholder="Leave empty to keep the current secret" /></label>
      <button className="secondary-button" type="button" disabled={updating || !editName.trim()} onClick={() => void submitEdit()}>
        {updating ? <LoaderCircle className="spin" size={16} /> : <Pencil size={16} />}Save {createLabelPrefix.toLowerCase()} credential
      </button>
      {updateError !== undefined && updateError !== null && <p className="credential-field-error">{messageFromError(updateError)}</p>}
    </div>}
  </div>;
}
