import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Check, GitBranch, LoaderCircle, LockKeyhole, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { api, messageFromError, type ListResult, type Project } from "../../api";
import { useCurrentUser } from "../../app/context";
import { useProjectsQuery } from "../../app/queries";
import { queryKeys } from "../../app/query";
import { ErrorNotice, LoadingState } from "../../shared/ui";

function CreateProjectForm({ onCancel }: { onCancel: () => void }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [description, setDescription] = useState("");
  const normalizedKey = key || name.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  const createProject = useMutation({
    mutationFn: () => api.createProject({ key: normalizedKey, name: name.trim(), description: description.trim() }),
    onSuccess: (project) => {
      queryClient.setQueryData<ListResult<Project>>(queryKeys.projects, (current) => ({
        items: [...(current?.items ?? []), project],
        total: (current?.total ?? 0) + 1,
      }));
      navigate(`/projects/${encodeURIComponent(project.key)}/incidents`, { replace: true });
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    createProject.mutate();
  };
  return <form className="create-project-form" onSubmit={submit}><div className="setup-card-title"><GitBranch size={20} /><div><h2>Create project</h2><p>The stable project key is used in API and browser paths and cannot be derived from a later name change.</p></div></div><label>Project name<input aria-label="Project name" value={name} onChange={(event) => setName(event.target.value)} placeholder="Checkout API" /></label><label>Project key<input aria-label="Project key" value={normalizedKey} onChange={(event) => setKey(event.target.value.toLowerCase())} placeholder="checkout-api" /></label><label>Description<textarea aria-label="Project description" value={description} onChange={(event) => setDescription(event.target.value)} /></label>{createProject.error && <ErrorNotice message={messageFromError(createProject.error)} />}<footer><button className="secondary-button" type="button" onClick={onCancel}>Cancel</button><button className="primary-button" type="submit" disabled={createProject.isPending || !name.trim() || normalizedKey.length < 3}>{createProject.isPending ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Create project</button></footer></form>;
}

export function ProjectDirectoryPage() {
  const user = useCurrentUser();
  const projects = useProjectsQuery();
  const navigate = useNavigate();
  if (projects.isPending) return <LoadingState label="Loading projects" />;
  if (projects.isError) return <main className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>Projects unavailable</h1><p>The project directory could not be loaded for the current session.</p></div></header><ErrorNotice message={messageFromError(projects.error)} onRetry={() => void projects.refetch()} /></main>;

  if (projects.data.items.length === 0) return <main className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>No accessible projects</h1><p>Projects are the boundary for members, Git context, collection configuration, events, incidents, and audit history.</p></div></header>{user.role === "admin" ? <section className="empty-projects"><GitBranch size={25} /><h2>Create the first project</h2><p>System administrators create projects. The creator becomes the first project administrator.</p><button className="primary-button" type="button" onClick={() => navigate("/projects/new")}><Plus size={16} />Create project</button></section> : <section className="empty-projects"><LockKeyhole size={25} /><h2>No project membership</h2><p>Ask a project administrator to add your username to a project.</p></section>}</main>;

  return <main className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>Switch projects</h1><p>Only projects returned for the current session are shown. Each role and capability comes from the server.</p></div></header><div className="project-list">{projects.data.items.map((project) => <Link className="project-row" key={project.id} to={`/projects/${encodeURIComponent(project.key)}/incidents`}><span className="project-initial">{project.name.slice(0, 1).toUpperCase()}</span><span><strong>{project.name}</strong><small>{project.key} · {project.role}</small></span><ArrowRight size={18} /></Link>)}{user.role === "admin" && <button type="button" className="create-project-row" onClick={() => navigate("/projects/new")}><span>+</span><div><strong>Create project</strong><small>Create a new persistent project boundary.</small></div><ArrowRight size={18} /></button>}</div></main>;
}

export function CreateProjectPage() {
  const user = useCurrentUser();
  const navigate = useNavigate();
  if (user.role !== "admin") return <Navigate to="/projects" replace />;

  return <main className="project-directory"><header><div><div className="eyebrow">Projects</div><h1>Create project</h1><p>Create a persistent boundary for members, configuration, observations, incidents, and audit history.</p></div></header><CreateProjectForm onCancel={() => navigate("/projects")} /></main>;
}

export function HomeRedirect() {
  const projects = useProjectsQuery();
  if (projects.isPending) return <LoadingState label="Loading projects" />;
  if (projects.isError || projects.data.items.length === 0) return <Navigate to="/projects" replace />;
  return <Navigate to={`/projects/${encodeURIComponent(projects.data.items[0].key)}/incidents`} replace />;
}
