import { Navigate, Outlet, useLocation } from "react-router-dom";
import { SessionContextProvider } from "../../app/context";
import { useSessionQuery } from "../../app/queries";
import { messageFromError } from "../../api";
import { ErrorNotice, LoadingState } from "../../shared/ui";

export function AuthenticatedRoute() {
  const session = useSessionQuery();
  const location = useLocation();

  if (session.isPending) return <LoadingState label="Loading Mendry" />;
  if (session.isError) return <main className="project-directory"><header><div><div className="eyebrow">Session</div><h1>Session unavailable</h1><p>The application could not verify the current session.</p></div></header><ErrorNotice message={messageFromError(session.error)} onRetry={() => void session.refetch()} /></main>;
  if (!session.data) return <Navigate to="/login" replace state={{ from: `${location.pathname}${location.search}` }} />;
  return <SessionContextProvider user={session.data}><Outlet /></SessionContextProvider>;
}
