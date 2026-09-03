import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, Play, RotateCcw } from "lucide-react";
import { api, ApiError, messageFromError, type RemediationContinuationInput } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { ErrorNotice } from "../../shared/ui";

type RemediationPanelProps = {
  projectKey: string;
  incidentId: string;
  generation: number;
};

const activeStates = new Set([
  "queued",
  "preparing_context",
  "diagnosing",
  "collecting_more_context",
  "planning",
  "running",
  "patching",
  "validating",
  "publishing",
]);

const continuationBlockingCodes = new Set(["remediation_conflict", "remediation_active", "remediation_unsupported", "forbidden"]);

function formatBudgetAmount(amount: Record<string, number> | undefined): string {
  if (!amount) return "n/a";
  const parts: string[] = [];
  if (typeof amount.modelCalls === "number") parts.push(`${amount.modelCalls} model calls`);
  if (typeof amount.toolCalls === "number") parts.push(`${amount.toolCalls} tools`);
  if (typeof amount.elapsedSeconds === "number") parts.push(`${Math.floor(amount.elapsedSeconds / 60)}m elapsed`);
  if (parts.length === 0) return "n/a";
  return parts.join(" · ");
}

function formatCheckpointAge(updatedAt: string): string {
  const updated = Date.parse(updatedAt);
  if (Number.isNaN(updated)) return "unknown";
  const minutes = Math.max(0, Math.floor((Date.now() - updated) / 60000));
  if (minutes < 1) return "<1m ago";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ${minutes % 60}m ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

export function RemediationPanel({ projectKey, incidentId, generation }: RemediationPanelProps) {
  const project = useCurrentProject();
  const queryClient = useQueryClient();
  const remediation = useQuery({
    queryKey: queryKeys.remediation(projectKey, incidentId),
    queryFn: ({ signal }) => api.getRemediation(projectKey, incidentId, signal),
    retry: false,
  });
  const missing = remediation.error instanceof ApiError && remediation.error.code === "remediation_not_found";
  const canWrite = project.capabilities.writeIncidents;

  const startRemediation = useMutation({
    mutationFn: () => api.startRemediation(projectKey, incidentId, generation),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.remediation(projectKey, incidentId) });
    },
  });
  const continueRemediation = useMutation({
    mutationFn: (input: RemediationContinuationInput) => api.retryRemediation(projectKey, incidentId, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.remediation(projectKey, incidentId) });
    },
  });
  const continuationBlocked = continueRemediation.error instanceof ApiError &&
    (continueRemediation.error.status === 403 || continuationBlockingCodes.has(continueRemediation.error.code));
  const review = remediation.data;

  const continueFromReview = () => {
    if (!review || !canWrite || !review.continuationAvailable || continuationBlocked || continueRemediation.isPending) return;
    continueRemediation.mutate({
      generation: review.generation,
      runId: review.runId,
      version: review.version,
    });
  };

  return (
    <section className="content-section remediation-section">
      <div className="panel-heading">
        <div>
          <h2>Remediation review</h2>
          <p>Diagnosis, evidence, risk, and the suggested unified diff for this incident generation.</p>
        </div>
      </div>
      {remediation.isPending && <p className="readonly-note">Loading remediation...</p>}
      {missing && (
        <div className="remediation-empty">
          <p className="readonly-note">No remediation run yet.</p>
          {canWrite ? (
            <button
              className="primary-button"
              type="button"
              disabled={startRemediation.isPending}
              onClick={() => startRemediation.mutate()}
            >
              {startRemediation.isPending ? <LoaderCircle className="spin" size={15} /> : <Play size={15} />}
              {startRemediation.isPending ? "Starting remediation..." : "Start remediation"}
            </button>
          ) : <p className="readonly-note">Viewer access is read-only.</p>}
        </div>
      )}
      {remediation.isError && !missing && <ErrorNotice message={messageFromError(remediation.error)} onRetry={() => void remediation.refetch()} />}
      {(startRemediation.error || continueRemediation.error) && (
        <ErrorNotice message={messageFromError(startRemediation.error ?? continueRemediation.error)} />
      )}
      {continueRemediation.isSuccess && (
        <p className="remediation-action-success" role="status">Continuation queued. Refreshing the latest attempt.</p>
      )}
      {review && (
        <div className="remediation-review">
          <div className="remediation-actions">
            {review.continuationAvailable && canWrite && (
              <button className="primary-button" type="button" disabled={continueRemediation.isPending || continuationBlocked} onClick={continueFromReview}>
                {continueRemediation.isPending ? <LoaderCircle className="spin" size={15} /> : <RotateCcw size={15} />}
                {continueRemediation.isPending ? "Continuing analysis..." : "Continue analysis"}
              </button>
            )}
            {!canWrite && <p className="readonly-note">Viewer access is read-only.</p>}
            {activeStates.has(review.status) && <p className="readonly-note">An analysis attempt is currently active.</p>}
            {!review.continuationAvailable && !activeStates.has(review.status) && (
              <p className="readonly-note">This remediation result cannot be continued.</p>
            )}
          </div>
          <dl className="incident-facts">
            <div><dt>Status</dt><dd>{review.status}</dd></div>
            <div><dt>Risk</dt><dd>{review.risk || "n/a"}</dd></div>
            <div><dt>Generation</dt><dd>{review.generation}</dd></div>
            <div><dt>Attempt</dt><dd>{review.attemptNumber}</dd></div>
            <div><dt>Origin</dt><dd>{review.origin || "legacy"}</dd></div>
            <div><dt>Version</dt><dd>{review.version}</dd></div>
            <div><dt>Terminal reason</dt><dd>{review.terminalReason || "None recorded"}</dd></div>
            <div><dt>Deployed commit</dt><dd>{review.deployedCommit || "n/a"}</dd></div>
            <div><dt>Loop mode</dt><dd>{review.agentLoopMode || "legacy"}{review.agentLoopPolicyVersion ? ` · policy v${review.agentLoopPolicyVersion}` : ""}</dd></div>
          </dl>
          {review.recovery && review.recovery.active && (
            <aside className="remediation-recovery" role="status" aria-label="Recovering / 自动恢复中">
              <h3>Recovering · 自动恢复中</h3>
              <p className="remediation-recovery-phase">
                Phase <strong>{review.checkpoint?.phase || review.status}</strong>
                {review.checkpoint && <> · checkpoint <strong>{review.checkpoint.sequence}</strong> ({formatCheckpointAge(review.checkpoint.updatedAt)})</>}
              </p>
              {(review.recovery.kind || review.recovery.reason) && (
                <p className="remediation-recovery-reason">
                  Recovery class <strong>{review.recovery.kind || "recoverable"}</strong>
                  {review.recovery.reason ? <> · reason code <strong>{review.recovery.reason}</strong></> : null}
                  {typeof review.recovery.attempt === "number" && review.recovery.attempt > 0 && <> · attempt <strong>{review.recovery.attempt}</strong></>}
                </p>
              )}
              {review.recovery.attemptedPathClasses.length > 0 && (
                <p className="remediation-recovery-paths">Tried path classes: {review.recovery.attemptedPathClasses.join(", ")}</p>
              )}
              {review.recovery.nextAction && (
                <p className="remediation-recovery-next">Next action: {review.recovery.nextAction}</p>
              )}
              {review.recovery.remainingBudget && (
                <p className="remediation-recovery-budget">
                  Remaining budget: {formatBudgetAmount(review.recovery.remainingBudget.remaining)} · unreserved {formatBudgetAmount(review.recovery.remainingBudget.unreserved)}
                </p>
              )}
              <p className="readonly-note">The run remains active and keeps recovering; this is not a manual-review conclusion.</p>
            </aside>
          )}
          {review.status === "blocked_manual_review" && review.manualSuggestion.trim() !== "" && (
            <aside className="remediation-manual-suggestion" aria-label="人工修复建议 / Manual fix suggestion">
              <h3>人工修复建议 / Manual fix suggestion</h3>
              <p>{review.manualSuggestion}</p>
              {review.diagnosis && review.diagnosis.missingEvidence.length > 0 && (
                <div className="remediation-missing-evidence">
                  <h4>缺失证据</h4>
                  <ul>
                    {review.diagnosis.missingEvidence.map((evidence) => <li key={evidence}>{evidence}</li>)}
                  </ul>
                </div>
              )}
            </aside>
          )}
          {review.attempts.length > 0 && (
            <div className="remediation-attempts">
              <h3>Attempts</h3>
              <ol>
                {review.attempts.map((attempt) => (
                  <li key={attempt.id}>
                    <div className="attempt-heading">
                      <strong>Attempt {attempt.attemptNumber}</strong>
                      <span>{attempt.origin || "legacy"} · {attempt.status}</span>
                    </div>
                    <p>Version {attempt.version} · context {attempt.contextVersion}</p>
                    {attempt.terminalReason && <p>Terminal reason: {attempt.terminalReason}</p>}
                    <time dateTime={attempt.createdAt}>{formatDate(attempt.createdAt)}</time>
                  </li>
                ))}
              </ol>
            </div>
          )}
          {review.diagnosis ? (
            <div className="remediation-diagnosis">
              <h3>Diagnosis</h3>
              <p><strong>{review.diagnosis.fixability}</strong> · confidence {review.diagnosis.confidence}</p>
              <p>{review.diagnosis.causalReasoning}</p>
              {review.diagnosis.evidenceRefs.length > 0 && (
                <p className="readonly-note">Evidence {review.diagnosis.evidenceRefs.join(", ")}</p>
              )}
              {review.diagnosis.recommendedNextAction && review.status !== "blocked_manual_review" && (
                <p className="readonly-note">{review.diagnosis.recommendedNextAction}</p>
              )}
            </div>
          ) : <p className="readonly-note">Diagnosis is not available yet.</p>}
          {review.plans.length > 0 && (
            <div className="remediation-plans">
              <h3>Plans</h3>
              <ul>
                {review.plans.map((plan) => (
                  <li key={plan.planId}>
                    <strong>{plan.intendedBehavior}</strong>
                    <span> · {plan.risk}{plan.recommended ? " · recommended" : ""}</span>
                    {plan.rationale && <p className="readonly-note">{plan.rationale}</p>}
                    {plan.affectedFiles.length > 0 && (
                      <p className="readonly-note">Files {plan.affectedFiles.join(", ")}</p>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {review.suggestedDiff ? (
            <pre className="remediation-diff">{review.suggestedDiff}</pre>
          ) : <p className="readonly-note">No suggested diff yet.</p>}
        </div>
      )}
    </section>
  );
}
