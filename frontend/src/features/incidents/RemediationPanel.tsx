import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, Play, RotateCcw } from "lucide-react";
import { api, ApiError, messageFromError, type RemediationContinuationInput } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { ErrorNotice } from "../../shared/ui";
import { parseUnifiedDiff, type DiffFile, type DiffLine } from "./remediationDiff";

type RemediationPanelProps = {
  projectKey: string;
  incidentId: string;
  generation: number;
};

type DiffSide = "original" | "changed";

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

function DiffLineView({ line }: { line: DiffLine | null }) {
  if (!line) {
    return <div className="remediation-diff-line empty" aria-hidden="true"><span className="remediation-diff-number" /><code> </code></div>;
  }
  return <div className={`remediation-diff-line ${line.kind}`}><span className="remediation-diff-number">{line.lineNumber ?? ""}</span><code>{line.text || " "}</code></div>;
}

function DiffPane({ file, side }: { file: DiffFile; side: DiffSide }) {
  const label = side === "original" ? "Original" : "Changed";
  return (
    <section className={`remediation-diff-pane ${side}`} aria-label={`${label} code for ${file.path}`}>
      <div className="remediation-diff-pane-header"><strong>{label}</strong><span>{side === "original" ? "Before" : "After"}</span></div>
      <div className="remediation-diff-lines">
        {file.rows.map((row, index) => <DiffLineView key={`${file.path}-${side}-${index}`} line={side === "original" ? row.original : row.changed} />)}
      </div>
    </section>
  );
}

function DiffViewer({ value }: { value: string }) {
  const parsed = parseUnifiedDiff(value);
  if (!parsed.parseable) return <pre className="remediation-diff remediation-diff-fallback">{value}</pre>;

  return <div className="remediation-diff-viewer">{parsed.files.map((file, index) => (
    <article className="remediation-diff-file" key={`${file.path}-${index}`}>
      <header className="remediation-diff-file-header">
        <div><span className="eyebrow">File</span><strong>{file.path}</strong></div>
        {file.header && <code>{file.header}</code>}
      </header>
      <div className="remediation-diff-panes">
        <DiffPane file={file} side="original" />
        <DiffPane file={file} side="changed" />
      </div>
    </article>
  ))}</div>;
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
      <div className="panel-heading remediation-panel-heading">
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
          <section className="remediation-overview" aria-labelledby="remediation-overview-heading">
            <header className="remediation-overview-header">
              <div>
                <h3 id="remediation-overview-heading">Run overview</h3>
                <p>Generation {review.generation} · Version {review.version}</p>
              </div>
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
            </header>

            <dl className="remediation-primary-facts" aria-label="Primary remediation run facts">
              <div className="remediation-status-fact"><dt>Status</dt><dd><span className="remediation-run-state">{review.status}</span></dd></div>
              <div><dt>Risk</dt><dd>{review.risk || "n/a"}</dd></div>
              <div><dt>Attempt</dt><dd>{review.attemptNumber}</dd></div>
              <div><dt>Origin</dt><dd>{review.origin || "legacy"}</dd></div>
              <div className="remediation-loop-fact"><dt>Loop mode</dt><dd>{review.agentLoopMode || "legacy"}{review.agentLoopPolicyVersion ? ` · policy v${review.agentLoopPolicyVersion}` : ""}</dd></div>
            </dl>
            <dl className="remediation-technical-facts" aria-label="Technical remediation run facts">
              <div><dt>Terminal reason</dt><dd>{review.terminalReason || "None recorded"}</dd></div>
              <div><dt>Deployed commit</dt><dd><code>{review.deployedCommit || "n/a"}</code></dd></div>
            </dl>
          </section>

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

          <div className="remediation-body">
            <section className="remediation-block remediation-diagnosis" aria-labelledby="remediation-diagnosis-heading">
              <div className="remediation-block-heading">
                <div><h3 id="remediation-diagnosis-heading">Diagnosis</h3><span>Evidence-backed assessment</span></div>
              </div>
              {review.diagnosis ? (
                <>
                  <div className="diagnosis-summary"><strong>{review.diagnosis.fixability}</strong><span>confidence {review.diagnosis.confidence}</span></div>
                  <p className="remediation-prose">{review.diagnosis.causalReasoning}</p>
                  {review.diagnosis.evidenceRefs.length > 0 && (
                    <p className="remediation-reference">Evidence <code>{review.diagnosis.evidenceRefs.join(", ")}</code></p>
                  )}
                  {review.diagnosis.recommendedNextAction && review.status !== "blocked_manual_review" && (
                    <p className="remediation-next-step">{review.diagnosis.recommendedNextAction}</p>
                  )}
                </>
              ) : <p className="readonly-note">Diagnosis is not available yet.</p>}
            </section>

            {review.attempts.length > 0 && (
              <section className="remediation-block remediation-attempts" aria-labelledby="remediation-attempts-heading">
                <div className="remediation-block-heading">
                  <div><h3 id="remediation-attempts-heading">Attempts</h3><span>{review.attempts.length} recorded</span></div>
                </div>
                <ol>
                  {review.attempts.map((attempt) => (
                    <li key={attempt.id}>
                      <div className="attempt-heading">
                        <div className="attempt-label"><strong>Attempt {attempt.attemptNumber}</strong><span>Version {attempt.version}</span></div>
                        <span className="attempt-state">{attempt.origin || "legacy"} · {attempt.status}</span>
                      </div>
                      <div className="attempt-details">
                        <span>Context {attempt.contextVersion}</span>
                        {attempt.terminalReason && <span>Terminal reason: {attempt.terminalReason}</span>}
                      </div>
                      <time dateTime={attempt.createdAt}>{formatDate(attempt.createdAt)}</time>
                    </li>
                  ))}
                </ol>
              </section>
            )}
          </div>

          {review.plans.length > 0 && (
            <section className="remediation-block remediation-plans" aria-labelledby="remediation-plans-heading">
              <div className="remediation-block-heading">
                <div><h3 id="remediation-plans-heading">Plans</h3><span>{review.plans.length} candidates</span></div>
              </div>
              <ol>
                {review.plans.map((plan, index) => (
                  <li className={plan.recommended ? "is-recommended" : undefined} key={plan.planId}>
                    <div className="remediation-plan-heading">
                      <div className="remediation-plan-title">
                        <span>Plan {index + 1}</span>
                        <strong>{plan.intendedBehavior}</strong>
                      </div>
                      <div className="remediation-plan-meta"><span>{plan.risk}</span>{plan.recommended && <span className="recommended">Recommended</span>}</div>
                    </div>
                    <div className="remediation-plan-body">
                      {plan.rationale && <p className="remediation-prose">{plan.rationale}</p>}
                      <div className="remediation-plan-details">
                        {plan.affectedFiles.length > 0 && (
                          <div className="remediation-plan-files"><span>Files</span><ul>{plan.affectedFiles.map((file) => <li key={file}><code>{file}</code></li>)}</ul></div>
                        )}
                        {plan.rollbackStrategy && <p className="remediation-plan-rollback"><span>Rollback</span>{plan.rollbackStrategy}</p>}
                      </div>
                    </div>
                  </li>
                ))}
              </ol>
            </section>
          )}

          {review.suggestedDiff ? (
            <section className="remediation-block remediation-diff-block" aria-labelledby="remediation-diff-heading">
              <div className="remediation-block-heading">
                <div><h3 id="remediation-diff-heading">Suggested diff</h3><span>Original / Changed</span></div>
              </div>
              <DiffViewer value={review.suggestedDiff} />
            </section>
          ) : <p className="readonly-note">No suggested diff yet.</p>}
        </div>
      )}
    </section>
  );
}
