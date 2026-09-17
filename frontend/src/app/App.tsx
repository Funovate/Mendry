import { QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AuthenticatedRoute } from "../features/auth/AuthenticatedRoute";
import { LoginPage } from "../features/auth/LoginPage";
import { ConfigurationEditorPage } from "../features/configuration/ConfigurationEditorPage";
import { ConfigurationPage } from "../features/configuration/ConfigurationPage";
import { IncidentsPage } from "../features/incidents/IncidentsPage";
import { ObservationsPage } from "../features/observations/ObservationsPage";
import { CreateProjectPage, HomeRedirect, ProjectDirectoryPage } from "../features/projects/ProjectDirectoryPage";
import { ProjectRoute } from "../features/projects/ProjectRoute";
import { AppShell } from "../layouts/AppShell";
import { AppErrorBoundary } from "./AppErrorBoundary";
import { createAppQueryClient } from "./query";
import { PipelinePage } from "../features/board/PipelinePage";

const queryClient = createAppQueryClient();

export default function App() {
  return <AppErrorBoundary><QueryClientProvider client={queryClient}><BrowserRouter><Routes>
    <Route path="/login" element={<LoginPage />} />
    <Route element={<AuthenticatedRoute />}>
      <Route index element={<HomeRedirect />} />
      <Route path="projects" element={<ProjectDirectoryPage />} />
      <Route path="projects/new" element={<CreateProjectPage />} />
      <Route path="projects/:projectKey" element={<ProjectRoute />}>
        <Route element={<AppShell />}>
          <Route index element={<Navigate to="incidents" replace />} />
          <Route path="incidents" element={<IncidentsPage />} />
          <Route path="incidents/:incidentId" element={<IncidentsPage />} />
          <Route path="pipeline" element={<PipelinePage />} />
          <Route path="board" element={<Navigate to="../pipeline" replace />} />
          <Route path="observations" element={<ObservationsPage />} />
          <Route path="configuration" element={<ConfigurationPage />} />
          <Route path="configuration/edit" element={<ConfigurationEditorPage />} />
        </Route>
      </Route>
    </Route>
    <Route path="*" element={<Navigate to="/" replace />} />
  </Routes></BrowserRouter></QueryClientProvider></AppErrorBoundary>;
}
