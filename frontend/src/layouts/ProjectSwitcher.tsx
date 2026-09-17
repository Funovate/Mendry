import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronsUpDown, LoaderCircle, Pencil, X } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, messageFromError, type ListResult, type Project } from "../api";
import { useCurrentProject } from "../app/context";
import { queryKeys } from "../app/query";
import { ErrorNotice, IconButton } from "../shared/ui";

export function ProjectSwitcher() {
  const project = useCurrentProject();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(project.name);
  const updateName = useMutation({
    mutationFn: () => api.updateProjectName(project.key, name.trim()),
    onSuccess: (updated) => {
      queryClient.setQueryData<ListResult<Project>>(queryKeys.projects, (current) => current ? {
        items: current.items.map((item) => item.key === updated.key ? updated : item),
        total: current.total,
      } : current);
      void queryClient.invalidateQueries({ queryKey: queryKeys.configuration(project.key) });
      setEditing(false);
    },
  });
  const startEdit = () => {
    setName(project.name);
    setEditing(true);
    updateName.reset();
  };
  const cancelEdit = () => {
    setName(project.name);
    setEditing(false);
    updateName.reset();
  };
  const save = () => {
    if (!name.trim()) return;
    updateName.mutate();
  };

  if (editing) {
    return <div className="project-switcher-edit">
      <span className="project-switcher-edit-copy">
        <small>Project name</small>
        <input
          aria-label="Project name"
          value={name}
          autoFocus
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") save();
            if (event.key === "Escape") cancelEdit();
          }}
        />
      </span>
      <button className="icon-button" type="button" aria-label="Cancel project name" title="Cancel project name" onClick={cancelEdit}><X size={15} /></button>
      <button
        className="icon-button"
        type="button"
        aria-label="Save project name"
        title="Save project name"
        disabled={updateName.isPending || !name.trim()}
        onClick={save}
      >
        {updateName.isPending ? <LoaderCircle className="spin" size={15} /> : <Check size={15} />}
      </button>
      {updateName.error && <ErrorNotice message={messageFromError(updateName.error)} />}
    </div>;
  }

  return <div className="project-switcher-row">
    <button type="button" className="project-switcher" aria-label={`Project ${project.name}`} onClick={() => navigate("/projects")}>
      <span className="project-switcher-meta">
        <strong>{project.name}</strong>
      </span>
      <ChevronsUpDown size={15} />
    </button>
    <IconButton label="Edit project name" onClick={startEdit}><Pencil size={14} /></IconButton>
  </div>;
}
