import { Navigate, Outlet, useParams } from "react-router-dom";
import { ProjectContextProvider } from "../../app/context";
import { useProjectsQuery } from "../../app/queries";
import { messageFromError } from "../../api";
import { ErrorNotice, LoadingState } from "../../shared/ui";

export function ProjectRoute() {
  const { projectKey } = useParams();
  const projects = useProjectsQuery();

  if (!projectKey) return <Navigate to="/projects" replace />;
  if (projects.isPending) return <LoadingState label="Loading project" />;
  if (projects.isError) return <main className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>Projects unavailable</h1><p>The project directory could not be loaded for the current session.</p></div></header><ErrorNotice message={messageFromError(projects.error)} onRetry={() => void projects.refetch()} /></main>;

  const project = projects.data.items.find((item) => item.key === projectKey);
  if (!project) return <main className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>Project not found</h1><p>This project is unavailable or no longer belongs to the current session.</p></div></header><a className="secondary-button" href="/projects">Open project directory</a></main>;
  return <ProjectContextProvider project={project}><Outlet /></ProjectContextProvider>;
}
