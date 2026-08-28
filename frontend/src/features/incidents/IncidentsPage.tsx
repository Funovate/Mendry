import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Check, ChevronLeft, SlidersHorizontal } from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, messageFromError, type ApiIncident, type IncidentStatus, type ListResult } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { ErrorNotice, IconButton, LoadingState, PageError, StatusPill } from "../../shared/ui";
import { RemediationPanel } from "./RemediationPanel";

const statuses: IncidentStatus[] = ["Open", "Recovered", "Closed"];

export function IncidentsPage() {
  const project = useCurrentProject();
  const { incidentId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("All");
  const incidents = useQuery({
    queryKey: queryKeys.incidents(project.key),
    queryFn: ({ signal }) => api.listIncidents(project.key, signal),
  });
  const updateStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: IncidentStatus }) => api.updateIncidentStatus(project.key, id, status),
    onSuccess: (updated) => queryClient.setQueryData<ListResult<ApiIncident>>(queryKeys.incidents(project.key), (current) => current ? { ...current, items: current.items.map((item) => item.id === updated.id ? updated : item) } : current),
  });
  const filtered = useMemo(() => (incidents.data?.items ?? []).filter((incident) => statusFilter === "All" || incident.status === statusFilter), [incidents.data, statusFilter]);
  const selected = incidents.data?.items.find((incident) => incident.id === incidentId) ?? null;

  if (incidents.isPending) return <LoadingState label="Loading incidents" />;
  if (incidents.isError) return <PageError message={messageFromError(incidents.error)} onRetry={() => void incidents.refetch()} />;

  return <div className="incidents-layout"><section className={`incident-list-pane ${selected ? "with-detail" : ""}`}><div className="list-header"><div><div className="eyebrow">{project.name}</div><h1>Incidents <span>{filtered.length}</span></h1></div></div><div className="filter-row"><select aria-label="Filter incident status" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option>All</option><option>Open</option><option>Recovered</option><option>Closed</option></select><span className="filter-chip"><SlidersHorizontal size={14} />Project scoped</span></div><div className="incident-list">{filtered.map((incident) => <button type="button" className={`incident-row ${selected?.id === incident.id ? "selected" : ""}`} onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/incidents/${encodeURIComponent(incident.id)}`)} key={incident.id}><div className="row-topline"><span className="incident-id">{incident.id}</span><span className="time">{formatDate(incident.lastSeen)}</span></div><strong>{incident.title}</strong><div className="row-meta"><StatusPill value={incident.status} /><StatusPill value={incident.priority} />{incident.muted && <span className="muted">Muted</span>}<span>{incident.occurrenceCount} events</span></div></button>)}{filtered.length === 0 && <div className="list-empty">No incidents match this project and status.</div>}</div></section>{incidentId && !selected ? <section className="empty-detail"><Activity size={28} /><h2>Incident not found</h2><p>The incident is unavailable in this project.</p><button className="secondary-button" type="button" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/incidents`, { replace: true })}>Back to incidents</button></section> : selected ? <section className="detail" aria-label="Incident detail"><div className="detail-header"><IconButton label="Back to incidents" onClick={() => navigate(`/projects/${encodeURIComponent(project.key)}/incidents`)}><ChevronLeft size={18} /></IconButton><div className="detail-heading"><div className="eyebrow">{selected.id} <span>Fingerprint {selected.fingerprint}</span></div><h1>{selected.title}</h1><div className="detail-meta"><StatusPill value={selected.status} /><StatusPill value={selected.priority} /><span>First seen {formatDate(selected.firstSeen)}</span><span>Last seen {formatDate(selected.lastSeen)}</span></div></div></div><div className="real-detail-grid"><section className="content-section"><h2>Occurrence summary</h2><dl className="incident-facts"><div><dt>Source</dt><dd>{selected.source}</dd></div><div><dt>Occurrences</dt><dd>{selected.occurrenceCount}</dd></div><div><dt>Hosts</dt><dd>{selected.hostCount}</dd></div><div><dt>Notification</dt><dd>{selected.notificationSummary || "None"}</dd></div></dl></section><section className="content-section"><div className="panel-heading"><div><h2>Lifecycle</h2><p>Available actions are derived from the project role returned by the server.</p></div></div>{updateStatus.error && <ErrorNotice message={messageFromError(updateStatus.error)} />}<div className="status-actions">{statuses.map((status) => <button className={status === selected.status ? "primary-button" : "secondary-button"} type="button" key={status} disabled={!project.capabilities.writeIncidents || updateStatus.isPending || status === selected.status} onClick={() => updateStatus.mutate({ id: selected.id, status })}>{status === selected.status && <Check size={15} />}{status}</button>)}</div>{!project.capabilities.writeIncidents && <p className="readonly-note">Viewer access is read-only.</p>}</section><RemediationPanel projectKey={project.key} incidentId={selected.id} generation={selected.lifecycleGeneration} /></div></section> : <section className="empty-detail"><Activity size={28} /><h2>Select an incident</h2><p>Choose a project incident to inspect its persisted state.</p></section>}</div>;
}
