import { useQueryClient } from "@tanstack/react-query";
import { Activity, ListFilter, LogOut, Menu, Settings2, ShieldCheck, Users, X } from "lucide-react";
import brandLogo from "../assets/mendry-logo-horizontal.svg";
import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { api } from "../api";
import { useCurrentProject, useCurrentUser } from "../app/context";
import { queryKeys } from "../app/query";
import { ProjectSwitcher } from "./ProjectSwitcher";
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

  const logout = async () => {
    try {
      await api.logout();
    } finally {
      queryClient.clear();
      queryClient.setQueryData(queryKeys.session, null);
      navigate("/login", { replace: true });
    }
  };

  return (
    <div className="app-shell">
      <aside className={`sidebar ${mobileOpen ? "mobile-open" : ""}`}>
        <div className="brand">
          <img className="brand-logo" src={brandLogo} alt="Mendry" width={130} height={32} />
          <IconButton label="Close navigation" onClick={() => setMobileOpen(false)}><X size={18} /></IconButton>
        </div>
        <ProjectSwitcher key={project.key} />
        <nav>
          {navItems.map(({ path, label, icon: Icon }) => (
            <NavLink to={path} onClick={() => setMobileOpen(false)} className={({ isActive }) => isActive ? "nav-active" : ""} key={path}>
              <Icon size={17} />{label}
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-foot">
          <div className="user-card">
            <div className="avatar">{user.username.slice(0, 1).toUpperCase()}</div>
            <div><strong>{user.username}</strong><small>{project.role}</small></div>
          </div>
          <IconButton label="Sign out" onClick={() => void logout()}><LogOut size={18} /></IconButton>
        </div>
      </aside>
      {mobileOpen && <button type="button" className="backdrop" aria-label="Close navigation" onClick={() => setMobileOpen(false)} />}
      <main>
        <header className="topbar">
          <div className="topbar-left">
            <IconButton label="Open navigation" onClick={() => setMobileOpen(true)}><Menu size={19} /></IconButton>
            <div className="breadcrumbs"><span>{project.name}</span></div>
          </div>
        </header>
        <Outlet />
      </main>
    </div>
  );
}
