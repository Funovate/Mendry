import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ArrowRight,
  CheckCircle2,
  Clock,
  Filter,
  Hand,
  LoaderCircle,
  Radio,
  RefreshCw,
  ScanSearch,
  Search,
  Server,
  ShieldAlert,
  ShieldCheck,
  Terminal,
  Waypoints,
  Wrench,
} from "lucide-react";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiError, type ApiIncident, type RemediationReview } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { LoadingState, PageError, StatusPill } from "../../shared/ui";
import "./pipeline.css";

export interface WorkflowStage {
  id: string;
  step: string;
  title: string;
  states: string[];
  prevName: string;
  nextName: string;
}

export const WORKFLOW_STAGES: WorkflowStage[] = [
  {
    id: "telemetry",
    step: "01",
    title: "Telemetry",
    states: ["queued", "preparing_context", "collecting_more_context"],
    prevName: "Trigger",
    nextName: "Diagnosis",
  },
  {
    id: "diagnosis",
    step: "02",
    title: "Diagnosis",
    states: ["diagnosing", "diagnosis_ready_for_review"],
    prevName: "Telemetry",
    nextName: "Remediation",
  },
  {
    id: "remediation",
    step: "03",
    title: "Remediation",
    states: ["planning", "patching", "running", "blocked_manual_review"],
    prevName: "Diagnosis",
    nextName: "Validation",
  },
  {
    id: "validation",
    step: "04",
    title: "Validation",
    states: ["validating", "publishing", "awaiting_human_review"],
    prevName: "Remediation",
    nextName: "Resolution",
  },
  {
    id: "resolution",
    step: "05",
    title: "Resolution",
    states: ["completed_non_code", "failed", "budget_exhausted"],
    prevName: "Validation",
    nextName: "Complete",
  },
];

export const STAGE_ICONS: Record<string, typeof Radio> = {
  telemetry: Radio,
  diagnosis: ScanSearch,
  remediation: Wrench,
  validation: ShieldCheck,
  resolution: CheckCircle2,
};

export const STATE_METADATA: Record<
  string,
  { label: string; prev: string; next: string; inProgress: boolean }
> = {
  queued: {
    label: "Queued",
    prev: "Triggered",
    next: "Context preparation",
    inProgress: true,
  },
  preparing_context: {
    label: "Preparing context",
    prev: "Queued",
    next: "Telemetry ingestion",
    inProgress: true,
  },
  collecting_more_context: {
    label: "Collecting evidence",
    prev: "Context",
    next: "Diagnosis",
    inProgress: true,
  },
  diagnosing: {
    label: "AI Diagnosing",
    prev: "Telemetry ready",
    next: "Remediation planning",
    inProgress: true,
  },
  planning: {
    label: "Planning fix",
    prev: "Diagnosed",
    next: "Patch generation",
    inProgress: true,
  },
  running: {
    label: "Patching code",
    prev: "Plan approved",
    next: "Validation",
    inProgress: true,
  },
  patching: {
    label: "Generating patch",
    prev: "Plan approved",
    next: "Validation",
    inProgress: true,
  },
  validating: {
    label: "Testing patch",
    prev: "Patch generated",
    next: "PR Review",
    inProgress: true,
  },
  publishing: {
    label: "Publishing branch",
    prev: "Tests passed",
    next: "Approval",
    inProgress: true,
  },
  diagnosis_ready_for_review: {
    label: "Review required",
    prev: "Diagnosis complete",
    next: "Approve plan",
    inProgress: false,
  },
  blocked_manual_review: {
    label: "Manual review blocked",
    prev: "Safety policy",
    next: "Operator triage",
    inProgress: false,
  },
  awaiting_human_review: {
    label: "Awaiting approval",
    prev: "Validation passed",
    next: "Merge",
    inProgress: false,
  },
  completed_non_code: {
    label: "Resolved (non-code)",
    prev: "Validated",
    next: "Closed",
    inProgress: false,
  },
  failed: {
    label: "Failed",
    prev: "Execution error",
    next: "Retry",
    inProgress: false,
  },
  budget_exhausted: {
    label: "Budget limit",
    prev: "Multi-round limit",
    next: "Resume",
    inProgress: false,
  },
};

export type ActionabilityCategory =
  | "running"
  | "needs_action"
  | "blocked"
  | "recoverable"
  | "terminal"
  | "unstarted";

export interface CardActionability {
  category: ActionabilityCategory;
  badgeText: string;
  badgeTone: "running" | "action" | "blocked" | "recoverable" | "terminal" | "unstarted";
  statusDescription: string;
  nextStepText: string;
  isAutoAdvancing: boolean;
  canAdvance: boolean;
  quickActionLabel?: string;
}

export function resolveActionability(
  status: string | undefined,
  remediationData?: RemediationReview | null
): CardActionability {
  if (!status) {
    return {
      category: "unstarted",
      badgeText: "Unstarted",
      badgeTone: "unstarted",
      statusDescription: "Ready for telemetry · Waiting for webhook trigger or manual start",
      nextStepText: "Manual start or inbound webhook",
      isAutoAdvancing: false,
      canAdvance: true,
      quickActionLabel: "Start Fix ➔",
    };
  }

  // Active AI Autonomous states: self-driving, progressing automatically
  if (
    [
      "queued",
      "preparing_context",
      "collecting_more_context",
      "diagnosing",
      "planning",
      "patching",
      "running",
      "validating",
      "publishing",
    ].includes(status)
  ) {
    const meta = STATE_METADATA[status];
    return {
      category: "running",
      badgeText: "Auto-Advancing",
      badgeTone: "running",
      statusDescription: `${meta?.label || status} · Autonomous AI in flight; auto-advances on completion`,
      nextStepText: meta?.next ? `Auto-advances to: ${meta.next}` : "Awaiting step completion",
      isAutoAdvancing: true,
      canAdvance: true,
    };
  }

  // Action required states: blocked on human decision (will NEVER advance without human)
  if (status === "diagnosis_ready_for_review") {
    return {
      category: "needs_action",
      badgeText: "Action Required",
      badgeTone: "action",
      statusDescription: "Remediation plan ready · Requires operator approval to execute",
      nextStepText: "Operator approval ➔ Patch & validation",
      isAutoAdvancing: false,
      canAdvance: true,
      quickActionLabel: "Review Plan ➔",
    };
  }

  if (status === "awaiting_human_review") {
    return {
      category: "needs_action",
      badgeText: "PR Review Needed",
      badgeTone: "action",
      statusDescription: "Patch validated · Awaiting operator review and merge",
      nextStepText: "Operator merge ➔ Release",
      isAutoAdvancing: false,
      canAdvance: true,
      quickActionLabel: "Review PR ➔",
    };
  }

  // Blocked / Security policy intercept
  if (status === "blocked_manual_review") {
    const suggestion = remediationData?.manualSuggestion?.trim();
    return {
      category: "blocked",
      badgeText: "Policy Blocked",
      badgeTone: "blocked",
      statusDescription: suggestion ? `Blocked: ${suggestion}` : "Protected path or safety policy tripped · Automation halted",
      nextStepText: "Cannot auto-advance · Operator takeover required",
      isAutoAdvancing: false,
      canAdvance: false,
      quickActionLabel: "Take Over ➔",
    };
  }

  // Recoverable states: Failed or Budget Exhausted with retryable/continuation
  const isRetryable = Boolean(
    remediationData?.retryable || remediationData?.continuationAvailable
  );

  if (status === "failed") {
    if (isRetryable) {
      return {
        category: "recoverable",
        badgeText: "Recoverable Error",
        badgeTone: "recoverable",
        statusDescription: remediationData?.terminalReason
          ? `Halted: ${remediationData.terminalReason}`
          : "Execution interrupted · Checkpoint preserved, retryable",
        nextStepText: "Retry or repair with current policy ➔ Resume",
        isAutoAdvancing: false,
        canAdvance: true,
        quickActionLabel: "Retry Run ➔",
      };
    }
    return {
      category: "terminal",
      badgeText: "Failed (Terminated)",
      badgeTone: "terminal",
      statusDescription: remediationData?.terminalReason
        ? `Terminated: ${remediationData.terminalReason}`
        : "Task failed and non-retryable",
      nextStepText: "Pipeline ended · Cannot advance",
      isAutoAdvancing: false,
      canAdvance: false,
    };
  }

  if (status === "budget_exhausted") {
    if (isRetryable) {
      return {
        category: "recoverable",
        badgeText: "Budget Exceeded",
        badgeTone: "recoverable",
        statusDescription: "Round budget reached · Checkpoint preserved, recoverable",
        nextStepText: "Extend budget or repair with settings ➔ Resume",
        isAutoAdvancing: false,
        canAdvance: true,
        quickActionLabel: "Resume Run ➔",
      };
    }
    return {
      category: "terminal",
      badgeText: "Budget Terminated",
      badgeTone: "terminal",
      statusDescription: "Maximum budget exhausted · Non-retryable",
      nextStepText: "Pipeline ended · Cannot advance",
      isAutoAdvancing: false,
      canAdvance: false,
    };
  }

  if (status === "completed_non_code") {
    return {
      category: "terminal",
      badgeText: "Resolved",
      badgeTone: "terminal",
      statusDescription: "Non-code mitigation or successfully closed",
      nextStepText: "Incident resolved · Pipeline completed",
      isAutoAdvancing: false,
      canAdvance: false,
    };
  }

  return {
    category: "running",
    badgeText: status,
    badgeTone: "running",
    statusDescription: status,
    nextStepText: "In flight",
    isAutoAdvancing: true,
    canAdvance: true,
  };
}

export interface PipelineItem {
  incident: ApiIncident;
  status?: string;
  remediationData?: RemediationReview | null;
  actionInfo: CardActionability;
}

export function PipelinePage() {
  const project = useCurrentProject();
  const queryClient = useQueryClient();

  const [searchQuery, setSearchQuery] = useState("");
  const [priorityFilter, setPriorityFilter] = useState("All");
  const [selectedStageId, setSelectedStageId] = useState<string | null>(null);
  const [actionFilter, setActionFilter] = useState<
    "all" | "running" | "needs_action" | "blocked" | "recoverable" | "unstarted"
  >("all");

  const {
    data: incidentsData,
    isLoading: isIncidentsLoading,
    error: incidentsError,
    refetch: refetchIncidents,
    isFetching: isIncidentsFetching,
  } = useQuery({
    queryKey: queryKeys.incidents(project.key, "Open"),
    queryFn: ({ signal }) =>
      api.listIncidents(project.key, { limit: 100, status: "Open" }, signal),
    refetchInterval: 25_000,
  });

  const incidents = useMemo(() => incidentsData?.items ?? [], [incidentsData]);

  const remediationQueries = useQueries({
    queries: incidents.map((incident) => ({
      queryKey: queryKeys.remediation(project.key, incident.id),
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        try {
          return await api.getRemediation(project.key, incident.id, signal);
        } catch (error) {
          if (error instanceof ApiError && error.status === 404) {
            return null;
          }
          throw error;
        }
      },
      refetchInterval: 25_000,
    })),
  });

  const loadedCount = remediationQueries.filter(
    (q) => q.isSuccess || q.isError
  ).length;
  const totalCount = remediationQueries.length;
  const isSyncing = isIncidentsLoading || (totalCount > 0 && loadedCount < totalCount);

  const handleRefresh = () => {
    void refetchIncidents();
    void queryClient.invalidateQueries({
      queryKey: ["project", project.key, "incidents"],
    });
  };

  const filteredIncidents = useMemo(() => {
    return incidents.filter((incident) => {
      if (priorityFilter !== "All" && incident.priority !== priorityFilter) {
        return false;
      }
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        return (
          incident.id.toLowerCase().includes(q) ||
          incident.title.toLowerCase().includes(q) ||
          incident.source.toLowerCase().includes(q)
        );
      }
      return true;
    });
  }, [incidents, priorityFilter, searchQuery]);

  const { stageGroups, unstartedGroup } = useMemo(() => {
    const groups = new Map<
      string,
      { stage: WorkflowStage; items: PipelineItem[] }
    >();

    WORKFLOW_STAGES.forEach((s) => {
      groups.set(s.id, { stage: s, items: [] });
    });

    const unstarted: PipelineItem[] = [];

    filteredIncidents.forEach((incident) => {
      const idx = incidents.findIndex((i) => i.id === incident.id);
      const remediationData = idx >= 0 ? remediationQueries[idx]?.data : null;
      const status = remediationData?.status;
      const actionInfo = resolveActionability(status, remediationData);

      const pipelineItem: PipelineItem = {
        incident,
        status,
        remediationData,
        actionInfo,
      };

      if (!status) {
        unstarted.push(pipelineItem);
        return;
      }

      const matched = WORKFLOW_STAGES.find((s) => s.states.includes(status));
      if (matched) {
        groups.get(matched.id)?.items.push(pipelineItem);
      } else {
        unstarted.push(pipelineItem);
      }
    });

    return {
      stageGroups: Array.from(groups.values()),
      unstartedGroup: unstarted,
    };
  }, [filteredIncidents, incidents, remediationQueries]);

  const activeInPipeline = useMemo(() => {
    return stageGroups.reduce((acc, g) => acc + g.items.length, 0);
  }, [stageGroups]);

  // Dynamic kinetic statistics for header filter tabs
  const actionCounts = useMemo(() => {
    let running = 0;
    let needsAction = 0;
    let blocked = 0;
    let recoverable = 0;
    let terminal = 0;
    const unstarted = unstartedGroup.length;

    stageGroups.forEach((g) => {
      g.items.forEach((item) => {
        switch (item.actionInfo.category) {
          case "running":
            running++;
            break;
          case "needs_action":
            needsAction++;
            break;
          case "blocked":
            blocked++;
            break;
          case "recoverable":
            recoverable++;
            break;
          case "terminal":
            terminal++;
            break;
        }
      });
    });

    return {
      all: activeInPipeline + unstarted,
      running,
      needsAction,
      blocked,
      recoverable,
      terminal,
      unstarted,
    };
  }, [stageGroups, unstartedGroup, activeInPipeline]);

  if (isIncidentsLoading && incidents.length === 0) {
    return (
      <div className="pipeline-page">
        <LoadingState label="Loading workflow..." />
      </div>
    );
  }

  if (incidentsError) {
    return (
      <div className="pipeline-page">
        <PageError
          message="Failed to load workflow data."
          onRetry={refetchIncidents}
        />
      </div>
    );
  }

  return (
    <div className="pipeline-page">
      {/* Visual Header */}
      <header className="pipeline-compact-header">
        <div className="header-title-row">
          <div className="header-brand-glyph">
            <Waypoints size={18} aria-hidden="true" />
          </div>
          <h1>Pipeline Flow</h1>

          {/* Actionability Filter Pills (Solves: What is advancing vs stuck?) */}
          <div className="header-action-filter-group" role="group" aria-label="Filter by execution state">
            <button
              type="button"
              className={`action-filter-pill ${actionFilter === "all" ? "is-active" : ""}`}
              onClick={() => setActionFilter("all")}
              title="All incidents"
            >
              <span>All</span>
              <span className="pill-count">{actionCounts.all}</span>
            </button>

            <button
              type="button"
              className={`action-filter-pill is-running ${actionFilter === "running" ? "is-active" : ""}`}
              onClick={() => setActionFilter(actionFilter === "running" ? "all" : "running")}
              title="Autonomous AI running · No intervention required"
            >
              <Activity size={12} className={actionCounts.running > 0 ? "spin" : ""} />
              <span>Auto-Advancing</span>
              <span className="pill-count">{actionCounts.running}</span>
            </button>

            <button
              type="button"
              className={`action-filter-pill is-action ${actionCounts.needsAction > 0 ? "has-urgent" : ""} ${
                actionFilter === "needs_action" ? "is-active" : ""
              }`}
              onClick={() => setActionFilter(actionFilter === "needs_action" ? "all" : "needs_action")}
              title="Operator review or approval required · Will not advance automatically"
            >
              <Hand size={12} className={actionCounts.needsAction > 0 ? "alert-shake" : ""} />
              <span>Action Required</span>
              <span className={`pill-count ${actionCounts.needsAction > 0 ? "urgent-count" : ""}`}>
                {actionCounts.needsAction}
              </span>
            </button>

            <button
              type="button"
              className={`action-filter-pill is-blocked ${actionCounts.blocked > 0 ? "has-urgent" : ""} ${
                actionFilter === "blocked" ? "is-active" : ""
              }`}
              onClick={() => setActionFilter(actionFilter === "blocked" ? "all" : "blocked")}
              title="Safety policy tripped · Automation halted, takeover required"
            >
              <ShieldAlert size={12} />
              <span>Policy Blocked</span>
              <span className={`pill-count ${actionCounts.blocked > 0 ? "urgent-count" : ""}`}>
                {actionCounts.blocked}
              </span>
            </button>

            {actionCounts.recoverable > 0 && (
              <button
                type="button"
                className={`action-filter-pill is-recoverable ${actionFilter === "recoverable" ? "is-active" : ""}`}
                onClick={() => setActionFilter(actionFilter === "recoverable" ? "all" : "recoverable")}
                title="Interrupted with checkpoint · Ready for retry or repair"
              >
                <RefreshCw size={12} />
                <span>Recoverable</span>
                <span className="pill-count">{actionCounts.recoverable}</span>
              </button>
            )}

            {actionCounts.unstarted > 0 && (
              <button
                type="button"
                className={`action-filter-pill is-unstarted ${actionFilter === "unstarted" ? "is-active" : ""}`}
                onClick={() => setActionFilter(actionFilter === "unstarted" ? "all" : "unstarted")}
                title="Unstarted incidents awaiting trigger"
              >
                <Radio size={12} />
                <span>Backlog</span>
                <span className="pill-count">{actionCounts.unstarted}</span>
              </button>
            )}
          </div>

          {isSyncing && (
            <div className="header-sync-pill">
              <LoaderCircle size={12} className="spin" />
              <span>{loadedCount}/{totalCount}</span>
            </div>
          )}
        </div>

        <div className="header-action-row">
          <div className="compact-search">
            <Search size={13} className="search-icon" aria-hidden="true" />
            <input
              type="text"
              placeholder="Filter ID..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              aria-label="Filter"
            />
            {searchQuery && (
              <button
                type="button"
                className="search-clear-btn"
                onClick={() => setSearchQuery("")}
              >
                ✕
              </button>
            )}
          </div>

          <div className="compact-filter">
            <Filter size={12} aria-hidden="true" />
            <select
              value={priorityFilter}
              onChange={(e) => setPriorityFilter(e.target.value)}
              aria-label="Priority"
            >
              <option value="All">All Priority</option>
              <option value="P1">P1</option>
              <option value="P2">P2</option>
              <option value="Info">Info</option>
            </select>
          </div>

          <button
            type="button"
            className="compact-refresh-btn"
            onClick={handleRefresh}
            disabled={isIncidentsFetching}
            title="Refresh"
          >
            <RefreshCw
              size={12}
              className={isIncidentsFetching ? "spin" : ""}
              aria-hidden="true"
            />
          </button>
        </div>
      </header>

      {/* Hero Visual Process Stepper Track (Subway / Circuit Flow Diagram) */}
      <div className="pipeline-visual-track-hero" role="region" aria-label="Workflow Diagram">
        <div className="visual-track-rail">
          {stageGroups.map(({ stage, items }, idx) => {
            const Icon = STAGE_ICONS[stage.id] || Radio;
            const count = items.length;
            const hasTasks = count > 0;
            const isSelected = selectedStageId === stage.id;
            const stageActionCount = items.filter(
              (i) => i.actionInfo.category === "needs_action"
            ).length;
            const stageBlockedCount = items.filter(
              (i) => i.actionInfo.category === "blocked"
            ).length;
            const stageRunningCount = items.filter(
              (i) => i.actionInfo.category === "running"
            ).length;

            return (
              <div key={stage.id} className="visual-stage-node-wrap">
                <button
                  type="button"
                  className={`visual-stage-node ${hasTasks ? "is-active" : "is-idle"} ${
                    isSelected ? "is-selected" : ""
                  } ${stageActionCount > 0 ? "has-action-alert" : ""} ${
                    stageBlockedCount > 0 ? "has-blocked-alert" : ""
                  }`}
                  onClick={() =>
                    setSelectedStageId(isSelected ? null : stage.id)
                  }
                  title={`Click to filter ${stage.title}`}
                >
                  <div className="node-icon-circle">
                    <Icon size={16} aria-hidden="true" />
                    {stageRunningCount > 0 && <span className="node-ring-glow" />}
                  </div>

                  <div className="node-caption">
                    <strong className="node-title">{stage.title}</strong>
                  </div>

                  {stageActionCount > 0 && (
                    <span className="node-alert-badge alert-action" title={`${stageActionCount} Action Needed`}>
                      <Hand size={9} />
                      <span>{stageActionCount}</span>
                    </span>
                  )}

                  {stageBlockedCount > 0 && (
                    <span className="node-alert-badge alert-blocked" title={`${stageBlockedCount} Blocked`}>
                      <ShieldAlert size={9} />
                      <span>{stageBlockedCount}</span>
                    </span>
                  )}

                  <div className={`node-count-badge ${hasTasks ? "has-count" : "zero-count"}`}>
                    {count}
                  </div>
                </button>

                {idx < stageGroups.length - 1 && (
                  <div className={`visual-track-link ${hasTasks ? "link-hot" : "link-cold"}`} aria-hidden="true">
                    <div className="link-bar" />
                    <div className="link-arrow">
                      <ArrowRight size={12} />
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {/* Stations Canvas */}
      <main className="pipeline-flow-scroll">
        <div className="pipeline-flow-canvas">
          {stageGroups
            .filter(({ stage }) => !selectedStageId || selectedStageId === stage.id)
            .map(({ stage, items }, stageIndex) => {
              const Icon = STAGE_ICONS[stage.id] || Radio;
              const visibleItems = items.filter((item) => {
                if (actionFilter !== "all" && item.actionInfo.category !== actionFilter) {
                  return false;
                }
                return true;
              });
              const hasVisibleItems = visibleItems.length > 0;
              const stageActionCount = visibleItems.filter(
                (i) => i.actionInfo.category === "needs_action"
              ).length;
              const stageBlockedCount = visibleItems.filter(
                (i) => i.actionInfo.category === "blocked"
              ).length;

              return (
                <section
                  key={stage.id}
                  className={`station-column ${hasVisibleItems ? "active-column" : "empty-column"}`}
                >
                  {/* Station Header Pill */}
                  <header className="station-pill-header">
                    <div className="station-pill-left">
                      <div className="station-glyph">
                        <Icon size={14} aria-hidden="true" />
                      </div>
                      <strong className="station-title">{stage.title}</strong>
                      {stageActionCount > 0 && (
                        <span className="station-alert-tag is-action" title={`${stageActionCount} require operator action`}>
                          <Hand size={10} />
                          <span>{stageActionCount} Action</span>
                        </span>
                      )}
                      {stageBlockedCount > 0 && (
                        <span className="station-alert-tag is-blocked" title={`${stageBlockedCount} blocked by policy`}>
                          <ShieldAlert size={10} />
                          <span>{stageBlockedCount} Blocked</span>
                        </span>
                      )}
                    </div>

                    <span className={`station-counter ${hasVisibleItems ? "active-counter" : ""}`}>
                      {visibleItems.length}
                    </span>
                  </header>

                  {/* Visual Task Cards */}
                  <div className="station-cards-container">
                    {visibleItems.map(({ incident, actionInfo }) => {
                      return (
                        <Link
                          key={incident.id}
                          to={`/projects/${encodeURIComponent(
                            project.key
                          )}/incidents/${encodeURIComponent(incident.id)}`}
                          className={`graphic-task-card card-tone-${actionInfo.badgeTone}`}
                          title={`Open ${incident.id}`}
                        >
                          {/* Incident identity and compact date */}
                          <div className="task-top-row">
                            <span className="task-id-badge">{incident.id}</span>
                            <StatusPill value={incident.priority} />
                            <span className="task-time-badge" title={formatDate(incident.lastSeen)}>
                              <Clock size={10} aria-hidden="true" />
                              {new Date(incident.lastSeen).toLocaleDateString("en-US", { month: "short", day: "numeric" })}
                            </span>
                          </div>

                          <h3 className="task-title-line" title={incident.title}>{incident.title}</h3>

                          {/* Advancement Status Banner: Tells user what can/cannot proceed */}
                          <div className={`task-advancement-banner is-${actionInfo.badgeTone}`}>
                            <strong className="advancement-heading">{actionInfo.badgeText}</strong>
                            <div className="advancement-desc">
                              {actionInfo.statusDescription}
                            </div>
                          </div>

                          {/* Graphical 5-Step Segment Gauge */}
                          <div className="task-gauge-row" aria-label={`Stage ${stageIndex + 1} of 5`}>
                            <div className="gauge-track">
                              {WORKFLOW_STAGES.map((_, gIdx) => {
                                const isFilled = gIdx < stageIndex;
                                const isCurrent = gIdx === stageIndex;
                                return (
                                  <span
                                    key={gIdx}
                                    className={`gauge-segment ${
                                      isFilled
                                        ? "is-passed"
                                        : isCurrent
                                        ? `is-current is-${actionInfo.badgeTone}`
                                        : "is-upcoming"
                                    }`}
                                  />
                                );
                              })}
                            </div>
                          </div>

                          {/* Micro Device Indicators & Quick Action Cue */}
                          <div className="task-bottom-row">
                            <div className="task-meta-glyphs">
                              <span className="glyph-chip">
                                <Terminal size={10} aria-hidden="true" />
                                {incident.source}
                              </span>
                              {incident.hostCount > 0 && (
                                <span className="glyph-chip">
                                  <Server size={10} aria-hidden="true" />
                                  {incident.hostCount}
                                </span>
                              )}
                              <span className="glyph-chip">
                                <Activity size={10} aria-hidden="true" />
                                {incident.occurrenceCount}
                              </span>
                            </div>

                            {actionInfo.quickActionLabel && (
                              <span className={`card-action-cue is-${actionInfo.badgeTone}`}>
                                {actionInfo.quickActionLabel}
                              </span>
                            )}
                          </div>
                        </Link>
                      );
                    })}

                    {visibleItems.length === 0 && (
                      <div className="station-empty-state">
                        <CheckCircle2 size={16} aria-hidden="true" />
                        <span>Empty</span>
                      </div>
                    )}
                  </div>
                </section>
              );
            })}

          {/* Unprocessed / Backlog Lane */}
          {unstartedGroup.length > 0 &&
            !selectedStageId &&
            (actionFilter === "all" || actionFilter === "unstarted") && (
              <section className="station-column unstarted-column">
                <header className="station-pill-header unstarted-header">
                  <div className="station-pill-left">
                    <div className="station-glyph">
                      <Radio size={14} aria-hidden="true" />
                    </div>
                    <strong className="station-title">Backlog (Unstarted)</strong>
                  </div>
                  <span className="station-counter">{unstartedGroup.length}</span>
                </header>

                <div className="station-cards-container">
                  {unstartedGroup.map(({ incident, actionInfo }) => (
                    <Link
                      key={incident.id}
                      to={`/projects/${encodeURIComponent(
                        project.key
                      )}/incidents/${encodeURIComponent(incident.id)}`}
                      className="graphic-task-card card-tone-unstarted unstarted-card"
                    >
                      <div className="task-top-row">
                        <span className="task-id-badge">{incident.id}</span>
                        <StatusPill value={incident.priority} />
                        <span className="task-time-badge" title={formatDate(incident.lastSeen)}>
                          <Clock size={10} aria-hidden="true" />
                          {new Date(incident.lastSeen).toLocaleDateString("en-US", { month: "short", day: "numeric" })}
                        </span>
                      </div>
                      <h3 className="task-title-line" title={incident.title}>{incident.title}</h3>
                      <div className="task-advancement-banner is-unstarted">
                        <strong className="advancement-heading">{actionInfo.badgeText}</strong>
                        <div className="advancement-desc">
                          {actionInfo.statusDescription}
                        </div>
                      </div>
                      <div className="task-bottom-row">
                        <div className="task-meta-glyphs">
                          <span className="glyph-chip">
                            <Terminal size={10} aria-hidden="true" />
                            {incident.source}
                          </span>
                        </div>
                        <span className="card-action-cue is-unstarted">
                          Start Fix ➔
                        </span>
                      </div>
                    </Link>
                  ))}
                </div>
              </section>
            )}
        </div>
      </main>
    </div>
  );
}
