import { useEffect, useRef, useState, type ChangeEvent } from "react";
import {
  composeSshPrivateKeyValue,
  inspectSshPrivateKeyDraft,
  inspectSshPrivateKeyFile,
  sshPrivateKeyInspectionMessage,
} from "../configuration";

export function SshPrivateKeyDraftField({
  value,
  onChange,
  textareaLabel,
  fileLabel,
  placeholder,
  passphrase = "",
  includePassphraseInSize = false,
}: {
  value: string;
  onChange: (value: string) => void;
  textareaLabel: string;
  fileLabel: string;
  placeholder?: string;
  passphrase?: string;
  includePassphraseInSize?: boolean;
}) {
  const [fileName, setFileName] = useState("");
  const [importError, setImportError] = useState<string | null>(null);
  const importGeneration = useRef(0);

  useEffect(() => () => {
    importGeneration.current += 1;
  }, []);

  useEffect(() => {
    if (value === "") setFileName("");
  }, [value]);

  const cancelImport = () => {
    importGeneration.current += 1;
  };

  const importFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.target;
    const file = input.files?.[0];
    input.value = "";
    if (!file) return;
    const generation = ++importGeneration.current;
    const result = await inspectSshPrivateKeyFile(file);
    if (generation !== importGeneration.current) return;
    if (!result.ok) {
      setImportError(sshPrivateKeyInspectionMessage(result.reason));
      setFileName("");
      return;
    }
    const nextStored = includePassphraseInSize ? composeSshPrivateKeyValue(result.text, passphrase) : result.text;
    const sized = inspectSshPrivateKeyDraft(nextStored);
    if (!sized.ok) {
      setImportError(sshPrivateKeyInspectionMessage(sized.reason));
      setFileName("");
      return;
    }
    setImportError(null);
    setFileName(file.name);
    onChange(result.text);
  };

  return <div className="ssh-key-draft">
    <label className="secret-input"><span>{textareaLabel}</span>
      <textarea aria-label={textareaLabel} value={value} placeholder={placeholder} onChange={(event) => {
        cancelImport();
        setImportError(null);
        setFileName("");
        onChange(event.target.value);
      }} />
    </label>
    <label className="ssh-key-file"><span>{fileLabel}</span>
      <input aria-label={fileLabel} type="file" accept=".pem,.key,text/plain" onChange={(event) => void importFile(event)} />
    </label>
    {fileName !== "" && <p className="ssh-key-file-name">{fileName}</p>}
    {importError !== null && <p className="credential-field-error">{importError}</p>}
  </div>;
}
