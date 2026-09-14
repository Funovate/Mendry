import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ArrowUpRight, BadgeCheck, Check, ChevronRight, ChevronsUpDown, Copy, FileCode2, Fingerprint, GitBranch, History, Layers, ListChecks, LoaderCircle, Play, RotateCcw, ScanSearch, ShieldCheck, Waypoints, Wrench } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { api, ApiError, messageFromError, type RemediationContinuationInput } from "../../api";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { ErrorNotice } from "../../shared/ui";
import { parseUnifiedDiff, type DiffFile, type DiffLine } from "./remediationDiff";

type RemediationPanelProps = {
  projectKey: string;
  incidentId: string;
  generation: number;
  fingerprint: string;
  notificationSummary: string;
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

export function DiffViewer({ value }: { value: string }) {
  const parsed = useMemo(() => parseUnifiedDiff(value), [value]);
  // Default collapsed: initially an empty set so all files are collapsed
  const [expandedFiles, setExpandedFiles] = useState<Set<number>>(() => new Set());
  const [isRawExpanded, setIsRawExpanded] = useState(false);

  useEffect(() => {
    setExpandedFiles(new Set());
    setIsRawExpanded(false);
  }, [value]);

  const fileCount = parsed.files.length;
  const allExpanded = fileCount > 0 && expandedFiles.size === fileCount;

  const toggleAll = () => {
    if (allExpanded) {
      setExpandedFiles(new Set());
    } else {
      setExpandedFiles(new Set(parsed.files.map((_, index) => index)));
    }
  };

  const toggleFile = (index: number) => {
    setExpandedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(index)) {
        next.delete(index);
      } else {
        next.add(index);
      }
      return next;
    });
  };

  return (
    <section className="remediation-block remediation-diff-block" aria-labelledby="remediation-diff-heading">
      <div className="remediation-block-heading">
        <div>
          <h3 id="remediation-diff-heading">
            <FileCode2 size={20} aria-hidden="true" />
            Suggested diff
            {fileCount > 0 && (
              <span className="remediation-diff-count" aria-label={`${fileCount} ${fileCount === 1 ? "file" : "files"}`}>
                {fileCount}
              </span>
            )}
          </h3>
          <div className="remediation-diff-actions">
            {fileCount > 0 && (
              <button
                type="button"
                className="remediation-diff-toggle-all"
                onClick={toggleAll}
                aria-label={allExpanded ? "Collapse all files" : "Expand all files"}
              >
                <ChevronsUpDown size={13} aria-hidden="true" />
                <span>{allExpanded ? "Collapse all" : "Expand all"}</span>
              </button>
            )}
            <span>Original / Changed</span>
          </div>
        </div>
      </div>

      {!parsed.parseable ? (
        <article className={`remediation-diff-file ${isRawExpanded ? "is-expanded" : "is-collapsed"}`}>
          <button
            type="button"
            className="remediation-diff-file-header"
            aria-expanded={isRawExpanded}
            onClick={() => setIsRawExpanded((prev) => !prev)}
          >
            <div className="remediation-diff-file-title">
              <ChevronRight
                size={15}
                className={`remediation-diff-chevron ${isRawExpanded ? "expanded" : ""}`}
                aria-hidden="true"
              />
              <strong>Raw diff output</strong>
            </div>
            <code>unformatted</code>
          </button>
          {isRawExpanded && <pre className="remediation-diff remediation-diff-fallback">{value}</pre>}
        </article>
      ) : parsed.files.length === 0 ? (
        <p className="readonly-note">No code changes in diff.</p>
      ) : (
        <div className="remediation-diff-viewer">
          {parsed.files.map((file, index) => {
            const isExpanded = expandedFiles.has(index);
            const added = file.rows.reduce((sum, r) => sum + (r.changed?.kind === "added" ? 1 : 0), 0);
            const removed = file.rows.reduce((sum, r) => sum + (r.original?.kind === "removed" ? 1 : 0), 0);
            const paneId = `diff-pane-${index}`;

            return (
              <article
                className={`remediation-diff-file ${isExpanded ? "is-expanded" : "is-collapsed"}`}
                key={`${file.path}-${index}`}
              >
                <button
                  type="button"
                  className="remediation-diff-file-header"
                  aria-expanded={isExpanded}
                  aria-controls={paneId}
                  onClick={() => toggleFile(index)}
                >
                  <div className="remediation-diff-file-title">
                    <ChevronRight
                      size={15}
                      className={`remediation-diff-chevron ${isExpanded ? "expanded" : ""}`}
                      aria-hidden="true"
                    />
                    <strong>{file.path}</strong>
                    {(added > 0 || removed > 0) && (
                      <span className="remediation-diff-file-stats" aria-label={`+${added} -${removed}`}>
                        {added > 0 && <span className="diff-stat-added">+{added}</span>}
                        {removed > 0 && <span className="diff-stat-removed">-{removed}</span>}
                      </span>
                    )}
                  </div>
                  {file.header && <code>{file.header}</code>}
                </button>
                {isExpanded && (
                  <div className="remediation-diff-panes" id={paneId}>
                    <DiffPane file={file} side="original" />
                    <DiffPane file={file} side="changed" />
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}
    </section>
  );
}

export function RemediationPanel({ projectKey, incidentId, generation, fingerprint, notificationSummary }: RemediationPanelProps) {
  const queryClient = useQueryClient();
  const [copiedCommit, setCopiedCommit] = useState<string | null>(null);
  const remediation = useQuery({
    queryKey: queryKeys.remediation(projectKey, incidentId),
    queryFn: ({ signal }) => api.getRemediation(projectKey, incidentId, signal),
    retry: false,
  });
  const missing = remediation.error instanceof ApiError && remediation.error.code === "remediation_not_found";
  const canWrite = true;

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
  const commitCopied = Boolean(review?.deployedCommit && copiedCommit === review.deployedCommit);

  const copyDeployedCommit = async () => {
    if (!review?.deployedCommit) return;
    try {
      await navigator.clipboard.writeText(review.deployedCommit);
      setCopiedCommit(review.deployedCommit);
      window.setTimeout(() => setCopiedCommit(null), 1600);
    } catch {
      // Clipboard access may be denied by the browser.
    }
  };

  const continueFromReview = () => {
    if (!review || !canWrite || !review.continuationAvailable || continuationBlocked || continueRemediation.isPending) return;
    continueRemediation.mutate({
      generation: review.generation,
      runId: review.runId,
      version: review.version,
    });
  };

  const incidentProperties = (
<section className="incident-properties" aria-labelledby="incident-properties-heading">
        <h3 id="incident-properties-heading"><Fingerprint size={16} aria-hidden="true" />Incident details</h3>
        <dl className="incident-facts">
          <div><dt>Fingerprint</dt><dd><code>{fingerprint}</code></dd></div>
          <div><dt>Notification</dt><dd>{notificationSummary || "None"}</dd></div>
        </dl>
      </section>
  );

  return (
    <section className="content-section remediation-section remediation-focused">
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
          <header className="remediation-summary-bar">
            <h2><Waypoints size={20} aria-hidden="true" />Remediation review</h2>
            <span className="remediation-state-label">{review.status.replaceAll("_", " ")}</span>
            <div className="remediation-actions">
                {review.continuationAvailable && canWrite && (
                  <button className="primary-button" type="button" disabled={continueRemediation.isPending || continuationBlocked} onClick={continueFromReview}>
                    {continueRemediation.isPending ? <LoaderCircle className="spin" size={15} /> : <RotateCcw size={15} />}
                    {continueRemediation.isPending ? "Continuing analysis..." : "Continue analysis"}
                  </button>
                )}

            </div>
          </header>
          <div className="remediation-columns">
          <div className="remediation-main">

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
                <div>
                  <h3 id="remediation-diagnosis-heading"><ScanSearch size={20} aria-hidden="true" />Diagnosis</h3>
                  <span>AI Root-Cause Synthesis</span>
                </div>
              </div>
              {review.diagnosis ? (
                <>
                  <div className="diagnosis-visual-summary">
                    <div className="diagnosis-overview">
                      <div className="diagnosis-confidence">
                        <svg className="confidence-ring" viewBox="0 0 64 64" aria-hidden="true"><circle className="confidence-track" cx="32" cy="32" r="27" /><circle className="confidence-value" cx="32" cy="32" r="27" pathLength="100" strokeDasharray={`${Math.max(0, Math.min(100, review.diagnosis.confidence * 100))} 100`} /></svg>
                        <span className="confidence-number">{Math.round(review.diagnosis.confidence * 100)}%</span>
                      </div>
                      <div className="diagnosis-summary"><strong><Wrench size={16} aria-hidden="true" />{review.diagnosis.fixability.replaceAll("_", " ")}</strong><span>Diagnosis confidence</span></div>
                    </div>
                    <div className="diagnosis-metric"><ScanSearch size={19} aria-hidden="true" /><strong>{review.diagnosis.evidenceRefs.length}</strong><span>Evidence references</span></div>
                    <div className="diagnosis-metric"><ListChecks size={19} aria-hidden="true" /><strong>{review.plans.length}</strong><span>Repair plans</span></div>
                  </div>
                  {review.diagnosis.recommendedNextAction && review.status !== "blocked_manual_review" && (
                    <div className="remediation-next-step"><h4><ArrowUpRight size={18} aria-hidden="true" />Recommended next step</h4><p>{review.diagnosis.recommendedNextAction}</p></div>
                  )}
                  <p className="remediation-prose">{review.diagnosis.causalReasoning}</p>
                </>
              ) : <p className="readonly-note">Diagnosis is not available yet.</p>}
            </section>
          </div>

          {review.plans.length > 0 && (
            <section className="remediation-block remediation-plans" aria-labelledby="remediation-plans-heading">
              <div className="remediation-block-heading">
                <div><h3 id="remediation-plans-heading"><GitBranch size={20} aria-hidden="true" />Plans</h3><span>{review.plans.length} candidates</span></div>
              </div>
              <ol>
                {review.plans.map((plan, index) => (
                  <li className={plan.recommended ? "is-recommended" : undefined} key={plan.planId}>
                    <div className="remediation-plan-heading">
                      <div className="remediation-plan-title">
                        <span className="plan-number"><Layers size={14} aria-hidden="true" />Plan {index + 1}</span>
                        <strong>{plan.intendedBehavior}</strong>
                      </div>
                      <div className="remediation-plan-meta"><span>{plan.risk}</span>{plan.recommended && <span className="recommended"><BadgeCheck size={14} aria-hidden="true" />Recommended</span>}</div>
                    </div>
                    <div className="remediation-plan-body">
                      {plan.rationale && <p className="remediation-prose">{plan.rationale}</p>}
                      <div className="remediation-plan-details">
                        {plan.affectedFiles.length > 0 && (
                          <div className="remediation-plan-files"><span><FileCode2 size={14} aria-hidden="true" />Files</span><ul>{plan.affectedFiles.map((file) => <li key={file}><code>{file}</code></li>)}</ul></div>
                        )}
                        {plan.rollbackStrategy && <p className="remediation-plan-rollback"><span><RotateCcw size={14} aria-hidden="true" />Rollback</span>{plan.rollbackStrategy}</p>}
                      </div>
                    </div>
                  </li>
                ))}
              </ol>
            </section>
          )}

          {review.suggestedDiff ? (
            <DiffViewer value={review.suggestedDiff} />
          ) : <p className="readonly-note">No suggested diff yet.</p>}
          </div>
          <aside className="remediation-context" aria-label="Run context">
          <section className="remediation-run-details" aria-labelledby="run-details-heading">
            <h3 id="run-details-heading"><ShieldCheck size={16} aria-hidden="true" />Run details</h3>
            <p className="run-version">Generation {review.generation} · Version {review.version}</p>
            <dl className="remediation-primary-facts" aria-label="Primary remediation run facts">
              <div><dt>Risk</dt><dd>{review.risk || "n/a"}</dd></div>
              <div><dt>Attempt</dt><dd>{review.attemptNumber}</dd></div>
              <div><dt>Origin</dt><dd>{review.origin || "legacy"}</dd></div>
              <div className="remediation-loop-fact"><dt>Loop mode</dt><dd>{review.agentLoopMode || "legacy"}{review.agentLoopPolicyVersion ? ` · policy v${review.agentLoopPolicyVersion}` : ""}</dd></div>
            </dl>
            <dl className="remediation-technical-facts" aria-label="Technical remediation run facts">
              <div><dt>Terminal reason</dt><dd>{review.terminalReason || "None recorded"}</dd></div>
              <div>
                <dt>Deployed commit</dt>
                <dd className="remediation-deployed-commit">
                  {review.deployedCommit ? (
                    <button
                      type="button"
                      className={`remediation-deployed-commit-button ${commitCopied ? "copied" : ""}`}
                      onClick={() => void copyDeployedCommit()}
                      title={`${commitCopied ? "Copied" : "Click to copy"}: ${review.deployedCommit}`}
                      aria-label={commitCopied ? "Deployed commit copied" : "Copy deployed commit"}
                    >
                      <code>{review.deployedCommit}</code>
                      {commitCopied ? <Check size={12} aria-hidden="true" /> : <Copy size={12} aria-hidden="true" />}
                    </button>
                  ) : <code>n/a</code>}
                </dd>
              </div>
            </dl>

            {!canWrite && <p className="readonly-note">Viewer access is read-only.</p>}
            {activeStates.has(review.status) && <p className="run-context-note">An analysis attempt is currently active.</p>}
            {!review.continuationAvailable && !activeStates.has(review.status) && <p className="run-context-note">This remediation result cannot be continued.</p>}
          </section>
            {review.attempts.length > 0 && (
              <section className="remediation-attempts" aria-labelledby="attempt-history-heading">
                <h3 id="attempt-history-heading"><History size={16} aria-hidden="true" />Attempt history <span>{review.attempts.length}</span></h3>
                <ol>
                  {review.attempts.map((attempt) => (
                    <li key={attempt.id}><span className="attempt-node" aria-hidden="true"><Activity size={12} /></span>
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

          {review.diagnosis && review.diagnosis.evidenceRefs.length > 0 && (
            <section className="diagnosis-evidence" aria-labelledby="diagnosis-evidence-heading">
              <h3 id="diagnosis-evidence-heading"><ScanSearch size={16} aria-hidden="true" />Diagnosis evidence <span>{review.diagnosis.evidenceRefs.length}</span></h3>
              <ol>{review.diagnosis.evidenceRefs.map((ref) => <li key={ref}><code>{ref}</code></li>)}</ol>
            </section>
          )}
            {incidentProperties}
          </aside>
          </div>


        </div>
      )}
      {!review && incidentProperties}

    </section>
  );
}