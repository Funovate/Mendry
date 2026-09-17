import { useQuery } from "@tanstack/react-query";
import { ApiError, api } from "../api";
import { queryKeys } from "./query";

export function useSessionQuery() {
  return useQuery({
    queryKey: queryKeys.session,
    queryFn: async ({ signal }) => {
      try {
        return await api.me(signal);
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) return null;
        throw error;
      }
    },
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export function useProjectsQuery() {
  return useQuery({
    queryKey: queryKeys.projects,
    queryFn: ({ signal }) => api.listProjects(signal),
  });
}
