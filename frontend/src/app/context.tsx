import { createContext, useContext, type ReactNode } from "react";
import type { CurrentUser, Project } from "../api";

const SessionContext = createContext<CurrentUser | null>(null);
const ProjectContext = createContext<Project | null>(null);

export function SessionContextProvider({ user, children }: { user: CurrentUser; children: ReactNode }) {
  return <SessionContext.Provider value={user}>{children}</SessionContext.Provider>;
}

export function ProjectContextProvider({ project, children }: { project: Project; children: ReactNode }) {
  return <ProjectContext.Provider value={project}>{children}</ProjectContext.Provider>;
}

export function useCurrentUser(): CurrentUser {
  const user = useContext(SessionContext);
  if (!user) throw new Error("Current user is unavailable outside the authenticated route.");
  return user;
}

export function useCurrentProject(): Project {
  const project = useContext(ProjectContext);
  if (!project) throw new Error("Current project is unavailable outside the project route.");
  return project;
}
