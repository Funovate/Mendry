import { Link, Navigate, Outlet, useParams } from "react-router-dom";
import brandLogo from "../../assets/mendry-logo-horizontal.svg";
import { ProjectContextProvider } from "../../app/context";
import { useProjectsQuery } from "../../app/queries";
import { messageFromError } from "../../api";
import { ErrorNotice, LoadingState } from "../../shared/ui";

export function ProjectRoute() {
  const { projectKey } = useParams();
  const projects = useProjectsQuery();

  if (!projectKey) return <Navigate to="/projects" replace />;
  if (projects.isPending) return <LoadingState label="Loading project" />;
  if (projects.isError) return (
    <main className="project-directory">
      <div className="project-directory-panel">
        <div className="directory-header-top">
          <img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={146} height={36} />
        </div>
        <header>
          <h1>Projects unavailable</h1>
          <p className="project-directory-subtitle">The project directory could not be loaded for the current session.</p>
        </header>
        <ErrorNotice message={messageFromError(projects.error)} onRetry={() => void projects.refetch()} />
      </div>
    </main>
  );

  const project = projects.data.items.find((item) => item.key === projectKey);
  if (!project) return (
    <main className="project-directory">
      <div className="project-directory-panel">
        <div className="directory-header-top">
          <img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={146} height={36} />
        </div>
        <header>
          <h1>Project not found</h1>
          <p className="project-directory-subtitle">This project is unavailable or no longer belongs to the current session.</p>
        </header>
        <Link className="primary-button" to="/projects">Open project directory</Link>
      </div>
    </main>
  );
  return <ProjectContextProvider project={project}><Outlet /></ProjectContextProvider>;
}
