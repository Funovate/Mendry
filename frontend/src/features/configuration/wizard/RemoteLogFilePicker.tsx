import { AlertTriangle, ChevronUp, File, FolderOpen, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, messageFromError, type SshLogFiles } from "../../../api";
import { useCurrentProject } from "../../../app/context";

function parentPath(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  const lastSlash = trimmed.lastIndexOf("/");
  if (lastSlash <= 0) return "/";
  return trimmed.slice(0, lastSlash);
}

export function RemoteLogFilePicker({
  sourceHost, sshPort, sshUser, sourceCredentialId, projectFolder, logPath, setLogPath,
}: {
  sourceHost: string;
  sshPort: number;
  sshUser: string;
  sourceCredentialId: string;
  projectFolder: string;
  logPath: string;
  setLogPath: (value: string) => void;
}) {
  const project = useCurrentProject();
  const [open, setOpen] = useState(false);
  const [browsePath, setBrowsePath] = useState("");
  const [result, setResult] = useState<SshLogFiles | null>(null);
  const [browseError, setBrowseError] = useState<unknown>(null);
  // Tracks the connection + path a request was made for, so a response for an outdated
  // request (superseded by a later navigation or a changed SSH connection field) is ignored.
  const requestToken = useRef(0);

  const connectionKey = JSON.stringify([project.key, sourceHost.trim(), sshPort, sshUser.trim(), sourceCredentialId]);
  const canBrowse = Boolean(sourceHost.trim() && sshUser.trim() && sourceCredentialId && Number.isInteger(sshPort) && sshPort >= 1 && sshPort <= 65535);

  const browse = useMutation({
    mutationFn: async ({ path, token }: { path: string; token: number }) => {
      const data = await api.browseSSHLogFiles(project.key, {
        host: sourceHost.trim(), port: sshPort, user: sshUser.trim(), credentialSecretId: sourceCredentialId, path,
      });
      return { data, token };
    },
    onSuccess: ({ data, token }) => {
      if (token !== requestToken.current) return;
      setResult(data);
      setBrowsePath(data.directory);
      setBrowseError(null);
    },
    onError: (error: unknown, variables) => {
      if (variables.token !== requestToken.current) return;
      setBrowseError(error);
      setResult(null);
    },
  });

  // Connection or manual log-path changes invalidate the current browser session.
  // Opening the browser must not invalidate the request started by the same click.
  const resetBrowse = browse.reset;
  useEffect(() => {
    requestToken.current += 1;
    setOpen(false);
    setResult(null);
    setBrowseError(null);
    setBrowsePath("");
    resetBrowse();
    return () => { requestToken.current += 1; };
  }, [connectionKey, logPath, resetBrowse]);

  const closeBrowser = () => {
    requestToken.current += 1;
    setOpen(false);
    setResult(null);
    setBrowseError(null);
    resetBrowse();
  };

  const navigateTo = (path: string) => {
    if (!canBrowse || !path.startsWith("/")) return;
    requestToken.current += 1;
    const token = requestToken.current;
    setBrowsePath(path);
    setResult(null);
    setBrowseError(null);
    browse.mutate({ path, token });
  };

  const startBrowsing = () => {
    setOpen(true);
    navigateTo(logPath.trim() || projectFolder.trim() || "/var/log");
  };

  const isLoading = browse.isPending;

  return <div className="remote-log-file-picker">
    <button type="button" className="secondary-button" onClick={() => (open ? closeBrowser() : startBrowsing())} disabled={!canBrowse}>
      <FolderOpen size={15} />
      <span>{open ? "Hide remote files" : "Browse remote files"}</span>
    </button>
    {!canBrowse && <p className="field-hint">Select an SSH private key and enter a host, user, and valid port to browse remote files.</p>}
    {open && <div className="remote-log-file-browser">
      <div className="remote-log-file-browser-nav">
        <button
          type="button"
          className="secondary-button"
          disabled={isLoading || !result || result.directory === "/"}
          onClick={() => navigateTo(parentPath(result?.directory ?? browsePath))}
          title="Go up one directory"
        >
          <ChevronUp size={14} />
          <span>Up</span>
        </button>
        <input
          aria-label="Remote directory path"
          value={browsePath}
          onChange={(event) => {
            requestToken.current += 1;
            setBrowsePath(event.target.value);
            setResult(null);
            setBrowseError(null);
            resetBrowse();
          }}
          onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); navigateTo(browsePath); } }}
        />
        <button type="button" className="secondary-button" disabled={isLoading || !browsePath.startsWith("/")} onClick={() => navigateTo(browsePath)}>
          {isLoading ? <LoaderCircle className="spin" size={14} /> : <FolderOpen size={14} />}
          <span>Go</span>
        </button>
      </div>
      {isLoading && <p className="field-hint"><LoaderCircle className="spin" size={13} /> Loading remote directory...</p>}
      {!isLoading && browseError !== null && <p className="credential-field-error" role="alert">{messageFromError(browseError)}</p>}
      {!isLoading && browseError === null && result && <>
        <p className="field-hint">{result.directory}</p>
        {result.entries.length === 0 && <p className="field-hint">This directory has no entries.</p>}
        {result.entries.length > 0 && <ul className="remote-log-file-list" aria-label="Remote log files">
          {result.entries.map((entry) => <li key={entry.path}>
            <button
              type="button"
              className="remote-log-file-entry"
              disabled={!entry.readable}
              onClick={() => {
                if (entry.kind === "directory") {
                  navigateTo(entry.path);
                  return;
                }
                setLogPath(entry.path);
                closeBrowser();
              }}
              title={!entry.readable ? "This entry is not accessible to the SSH user" : entry.path}
            >
              {entry.kind === "directory" ? <FolderOpen size={14} /> : <File size={14} />}
              <span>{entry.name}</span>
              {!entry.readable && <><span className="field-hint">Not accessible</span><AlertTriangle size={13} className="remote-log-file-warning" /></>}
            </button>
          </li>)}
        </ul>}
        {result.truncated && <p className="field-hint">This list is incomplete (up to 100 entries). Open a more specific directory or enter the full file path manually.</p>}
      </>}
    </div>}
  </div>;
}
