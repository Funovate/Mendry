import { useQuery } from "@tanstack/react-query";
import { api, messageFromError } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatTime } from "../../shared/format";
import { LoadingState, PageError } from "../../shared/ui";

export function ObservationsPage() {
  const project = useCurrentProject();
  const observations = useQuery({
    queryKey: queryKeys.observations(project.key),
    queryFn: ({ signal }) => api.listObservations(project.key, signal),
  });
  if (observations.isPending) return <LoadingState label="Loading event stream" />;
  if (observations.isError) return <PageError message={messageFromError(observations.error)} onRetry={() => void observations.refetch()} />;
  return <section className="settings-view"><div className="view-header"><div><h1>Event stream</h1></div></div><div className="event-list">{observations.data.items.map((item) => <article key={item.id}><span className={`level ${item.level.toLowerCase() === "warn" ? "warn" : ""}`}>{item.level}</span><code><time dateTime={item.occurredAt}>{formatTime(item.occurredAt)}</time> {item.message}</code><span>{item.service || item.sourceId}</span></article>)}{observations.data.items.length === 0 && <div className="table-empty">No observations have been collected for this project.</div>}</div></section>;
}
