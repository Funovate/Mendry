import { Navigate, Outlet, useLocation } from "react-router-dom";
import brandLogo from "../../assets/mendry-logo-horizontal.svg";
import { SessionContextProvider } from "../../app/context";
import { useSessionQuery } from "../../app/queries";
import { messageFromError } from "../../api";
import { ErrorNotice, LoadingState } from "../../shared/ui";

export function AuthenticatedRoute() {
  const session = useSessionQuery();
  const location = useLocation();

  if (session.isPending) return <LoadingState label="Loading Mendry" />;
  if (session.isError) return (
    <main className="project-directory">
      <div className="project-directory-panel">
        <div className="directory-header-top">
          <img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={146} height={36} />
        </div>
        <header>
          <h1>Session unavailable</h1>
          <p className="project-directory-subtitle">The application could not verify the current session.</p>
        </header>
        <ErrorNotice message={messageFromError(session.error)} onRetry={() => void session.refetch()} />
      </div>
    </main>
  );
  if (!session.data) return <Navigate to="/login" replace state={{ from: `${location.pathname}${location.search}` }} />;
  return <SessionContextProvider user={session.data}><Outlet /></SessionContextProvider>;
}
