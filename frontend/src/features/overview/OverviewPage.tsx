import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowRight, CheckCircle2, ChevronLeft, ChevronRight, Clock3, Crosshair, HelpCircle, RefreshCw, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { messageFromError } from "../../api";
import { useCurrentProject } from "../../app/context";
import { ErrorNotice, LoadingState } from "../../shared/ui";
import { getOverview, getOverviewTasks, overviewKeys, type OverviewTask, type Period, type TaskFilters, type TaskScope } from "./api";
import { dateTime, duration, number, stages, stateLabel, taskDuration } from "./format";
import { TaskFlow } from "./TaskFlow";
import { TokenTrend } from "./TokenTrend";
import { FailureBars, OutcomeDial, StageLoad } from "./OverviewVisuals";
import "./overview.css";

function browserTimezone() {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"; } catch { return "UTC"; }
}
function initialTimezone() {
  try { return localStorage.getItem("mendry.overview.timezone") === "UTC" ? "UTC" : browserTimezone(); } catch { return browserTimezone(); }
}

function TaskSummary({ task, projectKey, timezone, now, onClose }: {
  task: OverviewTask; projectKey: string; timezone: string; now: number; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { const element = dialog.current; element?.showModal(); return () => element?.close(); }, []);
  return <dialog ref={dialog} className="overview-dialog" onCancel={onClose} onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <header><div><span className="overview-eyebrow">{task.incidentId} · attempt {task.attemptNumber}</span><h2>{task.title}</h2></div><button className="icon-button" aria-label="Close task summary" onClick={onClose}><X size={19} /></button></header>

    <dl><div><dt>Status</dt><dd>{stateLabel(task.state)}</dd></div><div><dt>Attempt</dt><dd>{task.attemptNumber} · generation {task.generation}{!task.latest && " · historical attempt"}</dd></div><div><dt>Started</dt><dd>{dateTime(task.startedAt, timezone)}</dd></div><div><dt>Total elapsed</dt><dd>{taskDuration(task, now)}</dd></div><div><dt>Time in current phase</dt><dd>{taskDuration(task, now, true)}</dd></div><div><dt>Input / output Token</dt><dd>{task.usageRecorded ? `${number(task.tokensIn)} / ${number(task.tokensOut)}` : "Not recorded"}</dd></div><div><dt>Model</dt><dd>{task.model || "Not recorded"}</dd></div><div><dt>Run ID</dt><dd><code>{task.runId}</code></dd></div></dl>
    <Link className="primary-button" to={`/projects/${encodeURIComponent(projectKey)}/incidents/${encodeURIComponent(task.incidentId)}`}>Open incident{task.retryable ? " · retry available" : ""}<ArrowRight size={16} /></Link>
  </dialog>;
}

export function OverviewPage() {
  const project = useCurrentProject();
  // Reset task selection and pagination when switching projects.
  return <ProjectOverview key={project.key} projectKey={project.key} />;
}
function ProjectOverview({ projectKey }: { projectKey: string }) {
  const [timezone, setTimezone] = useState(initialTimezone);
  const [range, setRange] = useState<Period>("7d");
  const [filters, setFilters] = useState<TaskFilters>({ state: "", scope: "all", sort: "recent", page: 1 });
  const [selected, setSelected] = useState<{ task: OverviewTask; at: number } | null>(null);
  const overview = useQuery({ queryKey: overviewKeys.snapshot(projectKey, timezone, range),
    queryFn: ({ signal }) => getOverview(projectKey, timezone, range, signal),
    refetchInterval: 5000, refetchIntervalInBackground: false, refetchOnWindowFocus: "always" });
  const tasks = useQuery({ queryKey: overviewKeys.tasks(projectKey, filters),
    queryFn: ({ signal }) => getOverviewTasks(projectKey, filters, signal),
    refetchInterval: 5000, refetchIntervalInBackground: false, refetchOnWindowFocus: "always" });
  const onStage = useCallback((state: string) => {
    setFilters({ state, scope: "flow", sort: "recent", page: 1 });
  }, []);
  const selectTask = useCallback((task: OverviewTask) => setSelected({ task, at: Date.now() }), []);
  const selectScope = (scope: TaskScope) => {
    setFilters({ state: "", scope, sort: "recent", page: 1 });
    document.getElementById("overview-tasks")?.scrollIntoView({ block: "start" });
  };
  const refresh = () => { void overview.refetch(); void tasks.refetch(); };
  const changeTimezone = (value: string) => {
    setTimezone(value);
    try { localStorage.setItem("mendry.overview.timezone", value === "UTC" ? "UTC" : "browser"); } catch { /* Session preference still works without storage. */ }
  };
  const data = overview.data;
  const now = data ? Date.parse(data.generatedAt) : Date.now();
  return <div className="overview-page">
    <header className="overview-heading">
      <div className="hud-title"><Crosshair size={28} /><div><span className="overview-eyebrow">MENDRY / OPERATIONS</span><h1>Overview</h1></div></div>
      <div className="overview-controls">
        <span className="overview-freshness" role="status" title={data ? `Updated ${dateTime(data.generatedAt, timezone)} · auto-refresh 5s` : "Connecting"}><i className={overview.isError ? "stale" : ""} />{overview.isError ? "OFFLINE" : data ? "LIVE / 5s" : "CONNECTING"}</span>
        <select aria-label="Overview timezone" value={timezone} onChange={(event) => changeTimezone(event.target.value)}>{browserTimezone() !== "UTC" && <option value={browserTimezone()}>{browserTimezone()}</option>}<option value="UTC">UTC</option></select>
        <button className="secondary-button" aria-label="Refresh" title="Refresh" onClick={refresh} disabled={overview.isFetching || tasks.isFetching}><RefreshCw size={16} className={overview.isFetching ? "spin" : ""} /></button>
      </div>
    </header>
    {overview.error && <ErrorNotice message={`${data ? "Refresh failed. Showing the last successful snapshot. " : ""}${messageFromError(overview.error)}`} onRetry={refresh} />}
    {!data && overview.isPending && <LoadingState label="Loading project overview" />}
    {data && <>
      <section className="overview-stats" aria-label="Incident statistics">
        <article title="Distinct incidents with at least one ended attempt, across all time."><span><CheckCircle2 size={16} />Processed</span><strong>{number(data.counts.processed)}</strong></article>
        <article title={`Distinct incidents with an attempt completed today in ${timezone}.`}><span><Clock3 size={16} />Today</span><strong>{number(data.counts.todayProcessed)}</strong></article>
        <article className="highlight" title={`${data.counts.activeIncidents} incidents · ${data.counts.activeTasks} active attempts`}><span><Activity size={16} />Active</span><strong>{number(data.counts.activeIncidents)}</strong></article>
        <article className="positive" title="Latest attempt delivered a diagnosis, non-code conclusion or repair. Does not imply production recovery."><span><CheckCircle2 size={16} />Success</span><strong>{number(data.counts.successful)}</strong></article>
        <article className="negative" title={`Latest failed attempts, including ${data.counts.budgetExhausted} budget-exhausted attempts.`}><span><X size={16} />Failed</span><strong>{number(data.counts.failed)}</strong></article>
      </section>
      <div className="overview-outcomes">
        <span title="Incidents currently marked Recovered"><CheckCircle2 size={14} />Recovered <b>{number(data.counts.recovered)}</b></span>
        <button onClick={() => selectScope("attention")} title="Latest review or intervention state on open incidents"><Clock3 size={14} />Awaiting review <b>{number(data.counts.waiting)}</b><ArrowRight size={13} /></button>
        <details className="hud-help"><summary aria-label="Metric definitions"><HelpCircle size={15} /></summary><p>Processed counts distinct incidents with any ended attempt. Today uses completion time in {timezone}. Delivery success and failure use each incident’s latest attempt. Delivery does not confirm recovery. Only Recovered incidents count as recovered; Closed does not. These counts overlap.</p></details>
      </div>
      <div className="hud-command-grid">
        <section className="overview-panel overview-flow-panel"><header><h2><span className="hud-section-index">01</span>Live operations</h2><span className="overview-live-badge"><i />{number(data.counts.activeTasks)} ACTIVE</span></header>
          <TaskFlow overview={data} selected={filters.scope === "flow" ? filters.state : ""} onStage={onStage} onTask={selectTask} />

          <div className="overview-stage-shortcuts" aria-label="Filter workflow stages">{data.stages.map((stage) => <button key={stage.state} title={stateLabel(stage.state)} aria-pressed={filters.scope === "flow" && filters.state === stage.state} onClick={() => onStage(stage.state)}>{stages.find((item) => item.id === stage.state)?.short ?? stage.state}<b>{number(stage.count)}</b></button>)}</div>
        </section>
        <aside className="hud-side-panels">
          <section className="overview-panel"><header><h2><span className="hud-section-index">02</span>Delivery ratio</h2><span title="Success / (success + failure), using latest attempts; excludes active and blocked tasks." className="hud-info" tabIndex={0}><HelpCircle size={14} /></span></header><OutcomeDial overview={data} /></section>
          <section className="overview-panel"><header><h2><span className="hud-section-index">03</span>Stage load</h2><Activity size={15} /></header><StageLoad overview={data} onStage={onStage} /></section>
        </aside>
      </div>
      <div className="hud-analytics-grid">
        <section className="overview-panel hud-token-panel"><header><h2><span className="hud-section-index">04</span>Token consumption</h2><select aria-label="Overview time range" value={range} onChange={(event) => setRange(event.target.value as Period)}><option value="today">Today</option><option value="7d">7 days</option><option value="30d">30 days</option></select></header>
          <div className="overview-token-stats"><div><span>Total Token</span><strong>{number(data.tokens.input + data.tokens.output)}</strong></div><div><span><i className="hud-dot input" />Input</span><strong>{number(data.tokens.input)}</strong></div><div><span><i className="hud-dot output" />Output</span><strong>{number(data.tokens.output)}</strong></div><div title="Reported usage since the start of today; collection may cover only part of today."><span>Today</span><strong>{number(data.tokens.todayInput + data.tokens.todayOutput)}</strong></div></div>
          <TokenTrend overview={data} />
          {data.tokens.unrecordedTasks > 0 && <span className="overview-warning" title="Attempts with model calls but no recorded Token usage. Totals include only reported usage."><HelpCircle size={13} />{number(data.tokens.unrecordedTasks)} unreported</span>}
        </section>
        <section className="overview-panel hud-performance-panel"><header><h2><span className="hud-section-index">05</span>Processing time</h2><span className="hud-info" tabIndex={0} title={`${number(data.attention.sampleCount)} completed attempts since ${dateTime(data.attention.since, timezone)}. Includes queue time. Today uses a 7-day performance window.`}><HelpCircle size={14} /></span></header>
          <div className="hud-time-bars" aria-label="Median and P95 processing duration">
            {[{ label: "MEDIAN", value: data.attention.medianSeconds }, { label: "P95", value: data.attention.p95Seconds }].map((metric) => <div key={metric.label}><span>{metric.label}</span><strong>{duration(metric.value)}</strong><div className="hud-bar-track"><i style={{ width: metric.value === null ? "0%" : `${metric.value / Math.max(1, data.attention.p95Seconds ?? 1, data.attention.medianSeconds ?? 1) * 100}%` }} /></div></div>)}
          </div>
          <h3>Failure distribution <span>{number(data.attention.failures.reduce((sum, item) => sum + item.count, 0))}</span></h3><FailureBars overview={data} />
        </section>
      </div>
      <section className="overview-attention" aria-label="Operational attention">
        <article className="overview-panel hud-alert-panel"><header><h2><span className="hud-section-index">06</span>Attention</h2></header><div className="hud-alert-grid">{[{ label: "REVIEW", count: data.counts.waiting, scope: "attention" as const }, { label: "FAILED", count: data.counts.failed, scope: "failed" as const }, { label: "BUDGET", count: data.counts.budgetExhausted, scope: "budget" as const }].map((item) => <button key={item.scope} onClick={() => selectScope(item.scope)} className={item.scope}><strong>{number(item.count)}</strong><span>{item.label}<ArrowRight size={13} /></span></button>)}</div></article>
        <article className="overview-panel hud-running-panel"><header><h2><span className="hud-section-index">07</span>Longest in phase</h2><span title="Longest known stage duration first; elapsed time alone does not indicate timeout." className="hud-info" tabIndex={0}><HelpCircle size={14} /></span></header><div className="hud-running-list">{data.attention.longest.length ? data.attention.longest.map((task) => <button key={task.runId} className="overview-long-task" onClick={() => selectTask(task)} title={`${task.title} · ${stateLabel(task.state)} · total ${taskDuration(task, now)}`}><span><b>{task.incidentId}</b><small>{stages.find((stage) => stage.id === task.state)?.short ?? task.state}</small></span><strong>{taskDuration(task, now, true)}</strong><div className="hud-bar-track"><i style={{ width: task.stateEnteredAt ? `${Math.max(0, now - Date.parse(task.stateEnteredAt)) / Math.max(1, ...data.attention.longest.map((item) => item.stateEnteredAt ? now - Date.parse(item.stateEnteredAt) : 0)) * 100}%` : "0%" }} /></div></button>) : <div className="hud-standby"><span />STANDBY</div>}</div></article>
      </section>
    </>}
    <section className="overview-panel" id="overview-tasks"><header><div><h2><span className="hud-section-index">08</span>Task ledger</h2></div><div className="overview-task-filters"><label>Scope<select aria-label="Task scope" value={filters.scope} onChange={(event) => setFilters({ ...filters, scope: event.target.value as TaskScope, state: "", page: 1 })}><option value="all">All attempts</option><option value="flow">Active & latest</option><option value="attention">Needs attention</option><option value="failed">Latest failures</option><option value="budget">Budget exhausted</option></select></label><label>Stage<select aria-label="Task stage" value={filters.state} onChange={(event) => setFilters({ ...filters, state: event.target.value, page: 1 })}><option value="">All stages</option>{stages.map((stage) => <option key={stage.id} value={stage.id}>{stage.label}</option>)}{data?.stages.filter((stage) => !stages.some((known) => known.id === stage.state)).map((stage) => <option key={stage.state} value={stage.state}>{stateLabel(stage.state)}</option>)}</select></label><label>Sort<select aria-label="Task sort" value={filters.sort} onChange={(event) => setFilters({ ...filters, sort: event.target.value as TaskFilters["sort"], page: 1 })}><option value="recent">Most recent</option><option value="tokens">Highest Token</option></select></label></div></header>
      {tasks.error && <ErrorNotice message={`${tasks.data ? "Refresh failed. Showing the last task snapshot. " : ""}${messageFromError(tasks.error)}`} onRetry={() => void tasks.refetch()} />}
      {tasks.isPending && <LoadingState label="Loading tasks" />}
      {tasks.data && <><div className="overview-table-scroll"><table className="overview-task-table"><thead><tr><th>Incident / attempt</th><th>State</th><th>Input</th><th>Output</th><th>Total Token</th><th>Model</th><th>Elapsed</th></tr></thead><tbody>{tasks.data.items.map((task) => <tr key={task.runId}><td><button onClick={() => selectTask(task)}><b>{task.incidentId} · #{task.attemptNumber}</b><span>{task.title}</span><small>Generation {task.generation}{!task.latest ? " · historical" : ""}{task.retryable ? " · retry available" : ""}</small></button></td><td><span className={`overview-state ${task.state}`}>{stateLabel(task.state)}</span></td><td>{task.usageRecorded ? number(task.tokensIn) : "—"}</td><td>{task.usageRecorded ? number(task.tokensOut) : "—"}</td><td><b>{task.usageRecorded ? number(task.tokensIn + task.tokensOut) : "Not recorded"}</b></td><td>{task.model || "—"}</td><td>{taskDuration(task, Date.now())}</td></tr>)}</tbody></table>{tasks.data.items.length === 0 && <p className="overview-empty">No tasks match these filters.</p>}</div><footer className="overview-pagination"><span>{number(tasks.data.total)} attempts · page {filters.page} of {Math.max(1, Math.ceil(tasks.data.total / 20))}</span><div><button className="secondary-button" aria-label="Previous task page" disabled={filters.page <= 1} onClick={() => setFilters({ ...filters, page: filters.page - 1 })}><ChevronLeft size={16} />Previous</button><button className="secondary-button" aria-label="Next task page" disabled={filters.page * 20 >= tasks.data.total} onClick={() => setFilters({ ...filters, page: filters.page + 1 })}>Next<ChevronRight size={16} /></button></div></footer></>}
    </section>
    {selected && <TaskSummary task={selected.task} now={selected.at} projectKey={projectKey} timezone={timezone} onClose={() => setSelected(null)} />}
  </div>;
}
