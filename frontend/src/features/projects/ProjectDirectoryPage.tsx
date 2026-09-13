import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Check, GitBranch, LoaderCircle, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import brandLogo from "../../assets/mendry-logo-horizontal.svg";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { api, messageFromError, type ListResult, type Project } from "../../api";
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
      navigate(`/projects/${encodeURIComponent(project.key)}/overview`, { replace: true });
    },
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    createProject.mutate();
  };
  return <form className="create-project-form" onSubmit={submit}><label>Project name<input aria-label="Project name" value={name} onChange={(event) => setName(event.target.value)} placeholder="Checkout API" /></label><label>Project key<small className="field-hint">Used in URLs. Cannot be changed later.</small><input aria-label="Project key" value={normalizedKey} onChange={(event) => setKey(event.target.value.toLowerCase())} placeholder="checkout-api" /></label><label>Description<textarea aria-label="Project description" value={description} onChange={(event) => setDescription(event.target.value)} /></label>{createProject.error && <ErrorNotice message={messageFromError(createProject.error)} />}<footer><button className="secondary-button" type="button" onClick={onCancel}>Cancel</button><button className="primary-button" type="submit" disabled={createProject.isPending || !name.trim() || normalizedKey.length < 3}>{createProject.isPending ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}Create project</button></footer></form>;
}

export function ProjectDirectoryPage() {
  const projects = useProjectsQuery();
  const navigate = useNavigate();
  if (projects.isPending) return <LoadingState label="Loading projects" />;
  if (projects.isError) return <main className="project-directory"><img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={156} height={39} /><header><div><h1>Projects unavailable</h1></div></header><ErrorNotice message={messageFromError(projects.error)} onRetry={() => void projects.refetch()} /></main>;

  if (projects.data.items.length === 0) return <main className="project-directory"><img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={156} height={39} /><header><div><h1>No projects</h1></div></header><section className="empty-projects"><GitBranch size={25} /><h2>Create the first project</h2><button className="primary-button" type="button" onClick={() => navigate("/projects/new")}><Plus size={16} />Create project</button></section></main>;

  return <main className="project-directory"><img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={156} height={39} /><header><div><h1>Switch projects</h1></div></header><div className="project-list">{projects.data.items.map((project) => <Link className="project-row" key={project.id} to={`/projects/${encodeURIComponent(project.key)}/overview`}><span className="project-initial">{project.name.slice(0, 1).toUpperCase()}</span><span><strong>{project.name}</strong><small>{project.key}</small></span><ArrowRight size={18} /></Link>)}<button type="button" className="create-project-row" onClick={() => navigate("/projects/new")}><span>+</span><div><strong>Create project</strong></div><ArrowRight size={18} /></button></div></main>;
}

export function CreateProjectPage() {
  const navigate = useNavigate();

  return <main className="project-directory"><img className="brand-logo directory-logo" src={brandLogo} alt="Mendry" width={156} height={39} /><header><div><h1>Create project</h1></div></header><CreateProjectForm onCancel={() => navigate("/projects")} /></main>;
}

export function HomeRedirect() {
  const projects = useProjectsQuery();
  if (projects.isPending) return <LoadingState label="Loading projects" />;
  if (projects.isError || projects.data.items.length === 0) return <Navigate to="/projects" replace />;
  return <Navigate to={`/projects/${encodeURIComponent(projects.data.items[0].key)}/overview`} replace />;
}
