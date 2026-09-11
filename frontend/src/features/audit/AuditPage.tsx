import { useQuery } from "@tanstack/react-query";
import { api, messageFromError } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatDate } from "../../shared/format";
import { LoadingState, PageError } from "../../shared/ui";

export function AuditPage() {
  const project = useCurrentProject();
  const events = useQuery({
    queryKey: queryKeys.audit(project.key),
    queryFn: ({ signal }) => api.listAuditEvents(project.key, signal),
  });
  if (events.isPending) return <LoadingState label="Loading audit events" />;
  if (events.isError) return <PageError message={messageFromError(events.error)} onRetry={() => void events.refetch()} />;
  return <section className="settings-view"><div className="view-header"><div><h1>Audit</h1></div></div><div className="table-wrap"><table><thead><tr><th>Time</th><th>Action</th><th>Target</th><th>Summary</th></tr></thead><tbody>{events.data.items.map((event) => <tr key={event.id}><td>{formatDate(event.occurredAt)}</td><td>{event.action}</td><td>{event.targetType}</td><td>{event.summary}</td></tr>)}</tbody></table>{events.data.items.length === 0 && <div className="table-empty">No audit events have been recorded.</div>}</div></section>;
}
