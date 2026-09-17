import { Check, LoaderCircle, RefreshCw, ShieldAlert } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, messageFromError, type HotfixCheck, type ProjectConfiguration } from "../../../api";
import { useCurrentProject } from "../../../app/context";

type Props = {
  onEnabled: (policy: ProjectConfiguration["remediation"]) => void;
};

const activeStatuses = new Set<HotfixCheck["status"]>(["checking", "enabling"]);

function statusLabel(status: HotfixCheck["status"]): string {
  switch (status) {
    case "checking": return "Checking repository";
    case "needs_selection": return "Choose a service";
    case "ready": return "Ready to enable";
    case "enabling": return "Enabling automatic repair";
    case "enabled": return "Automatic repair enabled";
    case "blocked": return "Setup blocked";
    default: return "Not checked";
  }
}

export function AutoHotfixSetup({ onEnabled }: Props) {
  const project = useCurrentProject();
  const queryClient = useQueryClient();
  const [directory, setDirectory] = useState("");
  const check = useQuery({
    queryKey: ["project", project.key, "auto-hotfix-check"],
    queryFn: ({ signal }) => api.getAutoHotfixCheck(project.key, signal),
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status && activeStatuses.has(status) ? 2000 : false;
    },
  });
  const startCheck = useMutation({
    mutationFn: (selectedDirectory: string) => api.checkAutoHotfix(project.key, { directory: selectedDirectory }),
    onSuccess: (result) => queryClient.setQueryData(["project", project.key, "auto-hotfix-check"], result),
  });
  const enable = useMutation({
    mutationFn: (checkId: string) => api.enableAutoHotfix(project.key, checkId),
    onSuccess: (policy) => {
      onEnabled(policy);
      queryClient.invalidateQueries({ queryKey: ["project", project.key, "configuration-draft"] });
      queryClient.invalidateQueries({ queryKey: ["project", project.key, "configuration"] });
      queryClient.setQueryData(["project", project.key, "auto-hotfix-check"], (previous: HotfixCheck | undefined) => previous ? {
        ...previous,
        status: "enabled",
        message: "Automatic repair is enabled for new executions",
      } : previous);
    },
  });
  const state = check.data;
  const error = startCheck.error ?? enable.error ?? check.error;
  const busy = startCheck.isPending || enable.isPending || (state != null && activeStatuses.has(state.status));

  return <div className="step-content-group auto-hotfix-setup" aria-live="polite">
    <div className="section-heading-row">
      <div>
        <h3>Automatic setup</h3>
        <p className="field-hint">The platform checks the saved repository and reuses its Git credential.</p>
      </div>
      {state && <span className={`status-chip status-${state.status}`}>
        {activeStatuses.has(state.status) && <LoaderCircle size={14} className="spin" />}
        {state.status === "enabled" && <Check size={14} />}
        {state.status === "blocked" && <ShieldAlert size={14} />}
        {statusLabel(state.status)}
      </span>}
    </div>

    <div className="form-grid two-col">
      <label className="field-label">Service directory <span className="field-hint">Optional when the repository has one service</span>
        <input value={directory} onChange={(event) => setDirectory(event.target.value)} placeholder="backend/" disabled={busy} />
      </label>
      <div className="setup-actions inline-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={() => startCheck.mutate(directory.trim())}>
          <RefreshCw size={15} />
          {startCheck.isPending ? "Starting check..." : "Check repository"}
        </button>
      </div>
    </div>

    {state?.message && <p className={state.status === "blocked" ? "field-error" : "field-hint"}>{state.message}</p>}
    {state?.candidates.length ? <div className="candidate-list" role="group" aria-label="Detected services">
      {state.candidates.map((candidate) => <button type="button" className="secondary-button" key={`${candidate.directory}-${candidate.runtime}`} disabled={busy} onClick={() => {
        setDirectory(candidate.directory);
        startCheck.mutate(candidate.directory);
      }}>
        {candidate.directory || "."} <span>{candidate.runtime}</span>
      </button>)}
    </div> : null}
    {state?.validationSummary && <p className="field-hint">{state.validationSummary}</p>}
    {state?.credentialName && <p className="field-hint">Git credential: {state.credentialName}</p>}
    {state?.branchOnly && <p className="field-hint">The configured provider publishes a branch; merge request creation remains manual.</p>}
    {state?.status === "ready" && <div className="setup-actions">
      <button type="button" className="primary-button" disabled={enable.isPending} onClick={() => enable.mutate(state.id)}>
        <Check size={15} />
        {enable.isPending ? "Enabling..." : "Enable automatic repair"}
      </button>
    </div>}
    {error != null && <p className="field-error" role="alert">{messageFromError(error)}</p>}
  </div>;
}
