import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Check, ChevronDown, ChevronLeft, Clock3, Copy, LoaderCircle, Radio, Server, Terminal, Waypoints } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import brandMark from "../../assets/mendry-mark-reversed.svg";
import { api, messageFromError, type ApiIncident, type IncidentStatus } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { ErrorNotice, LoadingState, PageError, StatusPill } from "../../shared/ui";
import { RemediationPanel } from "./RemediationPanel";
import "./incident-workspace.css";

const statuses: IncidentStatus[] = ["Open", "Recovered", "Closed"];
const PAGE_SIZE = 25;

type IncidentPage = { items: ApiIncident[]; total: number };
type IncidentInfiniteData = { pages: IncidentPage[]; pageParams: unknown[] };

export function IncidentsPage() {
  const project = useCurrentProject();
  const { incidentId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("All");
  const [idCopied, setIdCopied] = useState(false);
  const listPaneRef = useRef<HTMLElement | null>(null);
  const sentinelRef = useRef<HTMLDivElement | null>(null);

  const incidents = useInfiniteQuery({
    queryKey: queryKeys.incidents(project.key, statusFilter),
    queryFn: ({ pageParam = 0, signal }) =>
      api.listIncidents(project.key, { limit: PAGE_SIZE, offset: pageParam, status: statusFilter }, signal),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      const loadedCount = allPages.reduce((acc, p) => acc + p.items.length, 0);
      return loadedCount < lastPage.total ? loadedCount : undefined;
    },
  });

  const allIncidents = useMemo(
    () => incidents.data?.pages.flatMap((page) => page.items) ?? [],
    [incidents.data]
  );
  const totalCount = incidents.data?.pages[0]?.total ?? allIncidents.length;

  const incidentInList = useMemo(
    () => allIncidents.find((incident) => incident.id === incidentId) ?? null,
    [allIncidents, incidentId]
  );
  const singleIncidentQuery = useQuery({
    queryKey: queryKeys.incident(project.key, incidentId ?? ""),
    queryFn: ({ signal }) => api.getIncident(project.key, incidentId!, signal),
    enabled: Boolean(incidentId && !incidentInList),
  });
  const selected = incidentInList ?? singleIncidentQuery.data ?? null;

  const updateStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: IncidentStatus }) =>
      api.updateIncidentStatus(project.key, id, status),
    onSuccess: (updated) => {
      queryClient.setQueriesData(
        { queryKey: ["project", project.key, "incidents"] },
        (current: IncidentInfiniteData | undefined) => {
          if (!current?.pages) return current;
          return {
            ...current,
            pages: current.pages.map((page: IncidentPage) => ({
              ...page,
              items: page.items.map((item: ApiIncident) => (item.id === updated.id ? updated : item)),
            })),
          };
        }
      );
      queryClient.setQueryData(queryKeys.incident(project.key, updated.id), updated);
      if (statusFilter !== "All" && statusFilter !== updated.status) {
        void incidents.refetch();
      }
    },
  });

  const handleCopyId = async () => {
    if (!selected) return;
    try {
      await navigator.clipboard.writeText(selected.id);
      setIdCopied(true);
      setTimeout(() => setIdCopied(false), 1600);
    } catch {
      // clipboard write may fail if permission denied
    }
  };

  const tryLoadNextPage = useCallback(() => {
    if (incidents.hasNextPage && !incidents.isFetchingNextPage) {
      void incidents.fetchNextPage();
    }
  }, [incidents]);

  // Mouse wheel listener: triggers next page when scrolling down near the bottom
  const handleWheel = useCallback(
    (event: React.WheelEvent<HTMLElement>) => {
      if (event.deltaY <= 0) return;
      const pane = listPaneRef.current;
      if (!pane) return;
      const scrollBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight;
      if (scrollBottom <= 180) {
        tryLoadNextPage();
      }
    },
    [tryLoadNextPage]
  );

  // Scroll event fallback
  const handleScroll = useCallback(() => {
    const pane = listPaneRef.current;
    if (!pane) return;
    const scrollBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight;
    if (scrollBottom <= 180) {
      tryLoadNextPage();
    }
  }, [tryLoadNextPage]);

  // IntersectionObserver for smooth auto-loading as sentinel enters viewport
  useEffect(() => {
    const sentinel = sentinelRef.current;
    const pane = listPaneRef.current;
    if (!sentinel || !pane) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const [entry] = entries;
        if (entry.isIntersecting) {
          tryLoadNextPage();
        }
      },
      {
        root: pane,
        rootMargin: "180px",
      }
    );

    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [tryLoadNextPage]);

  if (incidents.isPending) return <LoadingState label="Loading incidents" />;
  if (incidents.isError) return <PageError message={messageFromError(incidents.error)} onRetry={() => void incidents.refetch()} />;

  return (
    <div className="incidents-layout">
      <section
        ref={listPaneRef}
        className={`incident-list-pane ${selected ? "with-detail" : ""}`}
        onWheel={handleWheel}
        onScroll={handleScroll}
      >
        <div className="list-header">
          <div>
            <h1>Incidents <span className="incident-count-pill">{totalCount}</span></h1>
          </div>
          <button
            type="button"
            className="secondary-button"
            style={{ padding: "0 10px", fontSize: "11px", height: "28px" }}
            onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/pipeline`)}
            title="Open remediation workflow pipeline"
          >
            <Waypoints size={13} aria-hidden="true" />
            <span>Pipeline View</span>
          </button>
        </div>
        <div className="filter-row">
          <select aria-label="Filter incident status" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
            <option>All</option>
            <option>Open</option>
            <option>Recovered</option>
            <option>Closed</option>
          </select>
        </div>
        <div className="incident-list">
          {allIncidents.map((incident) => (
            <button
              type="button"
              className={`incident-row ${selected?.id === incident.id ? "selected" : ""}`}
              onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/incidents/${encodeURIComponent(incident.id)}`)}
              key={incident.id}
            >
              <div className="row-topline">
                <span className="incident-id">{incident.id}</span>
                <span className="time">{formatDate(incident.lastSeen)}</span>
              </div>
              <strong>{incident.title}</strong>
              <div className="row-meta">
                <StatusPill value={incident.status} />
                <StatusPill value={incident.priority} />
                {incident.muted && <span className="muted">Muted</span>}
                <span>{incident.occurrenceCount} events</span>
              </div>
            </button>
          ))}
          {allIncidents.length === 0 && !incidents.isPending && (
            <div className="list-empty">No incidents match this project and status.</div>
          )}
          <div ref={sentinelRef} className="list-sentinel" aria-hidden="true" />
          {incidents.isFetchingNextPage && (
            <div className="incident-list-loading-more" role="status">
              <LoaderCircle className="spin" size={15} aria-hidden="true" />
              <span>Loading more incidents...</span>
            </div>
          )}
          {!incidents.hasNextPage && allIncidents.length > 0 && (
            <div className="incident-list-end-notice">
              <span>All {totalCount} incidents loaded</span>
            </div>
          )}
        </div>
      </section>
      {incidentId && !selected && !singleIncidentQuery.isPending ? (
        <section className="empty-detail">
          <Activity size={28} />
          <h2>Incident not found</h2>
          <p>The incident is unavailable in this project.</p>
          <button className="secondary-button" type="button" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/incidents`, { replace: true })}>
            Back to incidents
          </button>
        </section>
      ) : selected ? (
    <section className="detail incident-workspace" aria-label="Incident detail">
      <header className="detail-header">
        <div className="detail-heading">
          <div className="detail-kicker-row">
            <nav className="incident-breadcrumb" aria-label="Incident navigation">
              <button
                className="incident-back-link"
                type="button"
                onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/incidents`)}
                title="Back to incident list"
                aria-label="Back to incidents"
              >
                <ChevronLeft size={14} className="back-chevron" aria-hidden="true" />
                <span>Incidents</span>
              </button>
              <span className="breadcrumb-separator" aria-hidden="true">/</span>
              <button
                type="button"
                className="eyebrow incident-id-badge"
                onClick={() => void handleCopyId()}
                title={idCopied ? "Copied ID to clipboard" : "Click to copy incident ID"}
                aria-label={`Incident ${selected.id}, click to copy ID`}
              >
                <span className={`incident-id-dot dot-${selected.status.toLowerCase()}`} aria-hidden="true" />
                <span>{selected.id}</span>
                {idCopied ? <Check size={12} className="id-copy-icon copied" aria-hidden="true" /> : <Copy size={12} className="id-copy-icon" aria-hidden="true" />}
              </button>
            </nav>
          </div>
          <h1>{selected.title}</h1>
          <div className="detail-meta">
            <StatusPill value={selected.status} /><StatusPill value={selected.priority} />
            <span><Terminal size={15} aria-hidden="true" />{selected.source}</span><span><Server size={15} aria-hidden="true" />{selected.hostCount} hosts</span><span><Radio size={15} aria-hidden="true" />{selected.occurrenceCount} occurrences</span>
          </div>
        </div>
        <div className="incident-header-side">
          <div className="incident-lifecycle">
            <label htmlFor="incident-status" className="sr-only">Incident status</label>
            <div className={`incident-status-control status-${selected.status.toLowerCase()}`}>
              <span className={`status-control-dot dot-${selected.status.toLowerCase()}`} aria-hidden="true" />
              <select
                id="incident-status"
                aria-label="Incident status"
                value={selected.status}
                disabled={updateStatus.isPending}
                onChange={(event) => updateStatus.mutate({ id: selected.id, status: event.target.value as IncidentStatus })}
              >
                {statuses.map((status) => (
                  <option key={status} value={status}>
                    {status === selected.status
                      ? status
                      : status === "Open"
                      ? "Reopen incident"
                      : status === "Recovered"
                      ? "Mark as recovered"
                      : "Close incident"}
                  </option>
                ))}
              </select>
              {updateStatus.isPending ? (
                <LoaderCircle className="status-control-icon spin" size={13} aria-hidden="true" />
              ) : (
                <ChevronDown className="status-control-icon" size={13} aria-hidden="true" />
              )}
            </div>
          </div>
          <dl className="incident-timestamps">
            <div>
              <dt><Clock3 size={13} aria-hidden="true" />First seen</dt>
              <dd>{formatDate(selected.firstSeen)}</dd>
            </div>
            <div>
              <dt>Last seen</dt>
              <dd>{formatDate(selected.lastSeen)}</dd>
            </div>
          </dl>
        </div>
        {updateStatus.error && <ErrorNotice message={messageFromError(updateStatus.error)} />}
      </header>
      <div className="incident-reading-area">
        <RemediationPanel key={selected.id} projectKey={project.key} incidentId={selected.id} generation={selected.lifecycleGeneration} fingerprint={selected.fingerprint} notificationSummary={selected.notificationSummary} />
      </div>
    </section>
  ) : (
    <section className="empty-detail incident-placeholder" aria-label="Select an incident">
      <div className="incident-welcome-card">
        <div className="welcome-art" aria-hidden="true">
          <div className="welcome-mark-box">
            <img src={brandMark} alt="" width={32} height={32} />
          </div>
        </div>
        <h2>Select an incident</h2>
        <p className="welcome-subtitle">
          Choose an incident from the feed to inspect telemetry anomalies, evaluate root cause diagnosis, and review remediation plans.
        </p>
        <div className="welcome-stats-grid">
          <div className="welcome-stat-card">
            <span className="welcome-stat-label">Project</span>
            <strong className="welcome-stat-value">{project.name}</strong>
            <span className="welcome-stat-meta">{project.key}</span>
          </div>
          <div className="welcome-stat-card">
            <span className="welcome-stat-label">Active Incidents</span>
            <strong className="welcome-stat-value">{allIncidents.filter((i) => i.status === "Open").length} open</strong>
            <span className="welcome-stat-meta">{totalCount} total tracked</span>
          </div>
        </div>
      </div>
    </section>
  )}
    </div>
  );
}
