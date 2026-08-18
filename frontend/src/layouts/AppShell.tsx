import { useQueryClient } from "@tanstack/react-query";
import { Activity, ChevronsUpDown, Gauge, ListFilter, LogOut, Menu, Settings2, ShieldCheck, UserRound, Users, X } from "lucide-react";
import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { api, type ApiIncident, type ListResult, type ProjectConfiguration } from "../api";
import { useCurrentProject, useCurrentUser } from "../app/context";
import { queryKeys } from "../app/query";
import { IconButton } from "../shared/ui";

const navItems = [
  { path: "incidents", label: "Incidents", icon: Activity },
  { path: "observations", label: "Event stream", icon: ListFilter },
  { path: "configuration", label: "Configuration", icon: Settings2 },
  { path: "members", label: "Members", icon: Users },
  { path: "audit", label: "Audit", icon: ShieldCheck },
] as const;

export function AppShell() {
  const user = useCurrentUser();
  const project = useCurrentProject();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [mobileOpen, setMobileOpen] = useState(false);
  const configuration = queryClient.getQueryData<ProjectConfiguration | null>(queryKeys.configuration(project.key));
  const incidents = queryClient.getQueryData<ListResult<ApiIncident>>(queryKeys.incidents(project.key));

  const logout = async () => {
    try {
      await api.logout();
    } finally {
      queryClient.clear();
      queryClient.setQueryData(queryKeys.session, null);
      navigate("/login", { replace: true });
    }
  };

  return <div className="app-shell"><aside className={`sidebar ${mobileOpen ? "mobile-open" : ""}`}><div className="brand"><div className="brand-mark"><Gauge size={20} /></div><span>fixthe</span><IconButton label="Close navigation" onClick={() => setMobileOpen(false)}><X size={18} /></IconButton></div><button type="button" className="project-switcher" onClick={() => navigate("/projects")}><span><small>Project</small><strong>{project.name}</strong></span><ChevronsUpDown size={16} /></button><nav>{navItems.map(({ path, label, icon: Icon }) => <NavLink to={path} onClick={() => setMobileOpen(false)} className={({ isActive }) => isActive ? "nav-active" : ""} key={path}><Icon size={17} />{label}{path === "incidents" && incidents && <span className="nav-count">{incidents.total}</span>}</NavLink>)}</nav><div className="sidebar-foot"><div className="environment"><span className="env-dot" />{configuration?.environment.name ?? "Project scoped"}</div><div className="user-card"><div className="avatar">{user.username.slice(0, 1).toUpperCase()}</div><div><strong>{user.username}</strong><small>{project.role} · {user.role === "admin" ? "system admin" : "local user"}</small></div></div></div></aside>{mobileOpen && <button type="button" className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}<main><header className="topbar"><div className="topbar-left"><IconButton label="Open navigation" onClick={() => setMobileOpen(true)}><Menu size={19} /></IconButton><div className="breadcrumbs"><span>{project.name}</span><span>/</span><strong>{project.key}</strong></div></div><div className="topbar-right"><span className="role-control"><UserRound size={15} /><span>{project.role}</span></span><IconButton label="Sign out" onClick={() => void logout()}><LogOut size={18} /></IconButton></div></header><Outlet /></main></div>;
}
