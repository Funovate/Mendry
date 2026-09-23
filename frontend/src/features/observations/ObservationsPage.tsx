import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, ListFilter, RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { api, messageFromError } from "../../api";
import { useCurrentProject } from "../../app/context";
import { queryKeys } from "../../app/query";
import { formatTime } from "../../shared/format";
import { LoadingState, PageError } from "../../shared/ui";
import "./observations.css";

const PAGE_SIZE = 10;

export function ObservationsPage() {
  const project = useCurrentProject();
  const [page, setPage] = useState(1);
  const [levelFilter, setLevelFilter] = useState<string>("ALL");
  const [searchQuery, setSearchQuery] = useState("");

  const observations = useQuery({
    queryKey: queryKeys.observations(project.key, page),
    queryFn: ({ signal }) => api.listObservations(project.key, { limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE }, signal),
    placeholderData: (previousData) => previousData,
  });

  const allItems = useMemo(() => observations.data?.items ?? [], [observations.data]);
  const totalCount = observations.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));
  const rangeStart = totalCount === 0 ? 0 : (page - 1) * PAGE_SIZE + 1;
  const rangeEnd = Math.min(page * PAGE_SIZE, totalCount);

  const filteredItems = useMemo(() => {
    return allItems.filter((item) => {
      const matchesLevel =
        levelFilter === "ALL" ||
        item.level.toUpperCase() === levelFilter.toUpperCase();
      const matchesSearch =
        !searchQuery.trim() ||
        item.message.toLowerCase().includes(searchQuery.toLowerCase()) ||
        (item.service && item.service.toLowerCase().includes(searchQuery.toLowerCase())) ||
        (item.sourceId && item.sourceId.toLowerCase().includes(searchQuery.toLowerCase()));
      return matchesLevel && matchesSearch;
    });
  }, [allItems, levelFilter, searchQuery]);

  if (observations.isPending) return <LoadingState label="Loading event stream" />;
  if (observations.isError) {
    return (
      <PageError
        message={messageFromError(observations.error)}
        onRetry={() => void observations.refetch()}
      />
    );
  }

  const countsByLevel = allItems.reduce(
    (acc, item) => {
      const lvl = item.level.toUpperCase();
      acc[lvl] = (acc[lvl] || 0) + 1;
      return acc;
    },
    {} as Record<string, number>
  );

  return (
    <section className="settings-view observations-view">
      <div className="view-header">
        <div>
          <h1>Event stream</h1>
          <p className="view-header-subtitle">
            Real-time telemetry observations, ingested service logs, and collector events.
          </p>
        </div>
        <div className="observations-header-actions">
          <div className="observations-stats-pill">
            <span className="stats-dot" aria-hidden="true" />
            <span className="stats-pill-text">
              <strong>{totalCount}</strong> events collected
            </span>
          </div>
          <button
            type="button"
            className="secondary-button"
            onClick={() => void observations.refetch()}
            title="Refresh event stream"
          >
            <RefreshCw size={14} />
            Refresh
          </button>
        </div>
      </div>

      {allItems.length > 0 && (
        <div className="observations-filter-toolbar">
          <div className="observations-level-tabs" role="tablist" aria-label="Filter by level">
            {(["ALL", "ERROR", "WARN", "INFO"] as const).map((lvl) => {
              const count = lvl === "ALL" ? allItems.length : countsByLevel[lvl] || 0;
              const isActive = levelFilter === lvl;
              return (
                <button
                  key={lvl}
                  type="button"
                  role="tab"
                  aria-selected={isActive}
                  className={`level-tab ${isActive ? "active" : ""}`}
                  onClick={() => setLevelFilter(lvl)}
                >
                  <span>{lvl}</span>
                  <small className="tab-badge">{count}</small>
                </button>
              );
            })}
          </div>

          <div className="observations-search-box">
            <input
              type="search"
              placeholder="Filter current page..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              aria-label="Search events"
            />
          </div>
        </div>
      )}

      <div className="event-list-card">
        {allItems.length > 0 && (
          <div className="event-list-head">
            <span className="col-level">LEVEL</span>
            <span className="col-time">TIME</span>
            <span className="col-message">MESSAGE</span>
            <span className="col-service">SERVICE / SOURCE</span>
          </div>
        )}

        <div className="event-list">
          {filteredItems.map((item) => {
            const lvlUpper = item.level.toUpperCase();
            const levelClass =
              lvlUpper === "ERROR"
                ? "error"
                : lvlUpper === "WARN"
                ? "warn"
                : "info";

            return (
              <article key={item.id} className={`event-row level-${levelClass}`}>
                <span className={`level ${item.level.toLowerCase() === "warn" ? "warn" : ""} level-tag ${levelClass}`}>
                  {item.level}
                </span>
                <code className="event-time">
                  <time dateTime={item.occurredAt}>{formatTime(item.occurredAt)}</time>
                </code>
                <code className="event-message">
                  {item.message}
                </code>
                <span className="event-service-tag">
                  {item.service || item.sourceId}
                </span>
              </article>
            );
          })}

          {allItems.length === 0 && (
            <div className="table-empty">
              <ListFilter size={24} className="empty-icon" />
              <h3>No observations collected</h3>
              <p>No observations have been collected for this project.</p>
            </div>
          )}

          {allItems.length > 0 && filteredItems.length === 0 && (
            <div className="table-empty">
              <p>No events match the current filter criteria.</p>
            </div>
          )}
        </div>
      </div>

      {totalCount > 0 && (
        <nav className="observations-pagination" aria-label="Event stream pagination">
          <p>
            Showing <strong>{rangeStart}-{rangeEnd}</strong> of <strong>{totalCount}</strong>
          </p>
          <div className="pagination-controls">
            <button
              type="button"
              className="icon-button"
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              disabled={page === 1 || observations.isFetching}
              aria-label="Previous page"
              title="Previous page"
            >
              <ChevronLeft size={16} />
            </button>
            <span aria-live="polite">
              Page <strong>{page}</strong> of <strong>{totalPages}</strong>
            </span>
            <button
              type="button"
              className="icon-button"
              onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
              disabled={page >= totalPages || observations.isFetching}
              aria-label="Next page"
              title="Next page"
            >
              <ChevronRight size={16} />
            </button>
          </div>
        </nav>
      )}
    </section>
  );
}
