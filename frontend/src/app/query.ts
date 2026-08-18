import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";

export const queryKeys = {
  session: ["session"] as const,
  projects: ["projects"] as const,
  project: (projectKey: string) => ["project", projectKey] as const,
  configuration: (projectKey: string) => ["project", projectKey, "configuration"] as const,
  secrets: (projectKey: string) => ["project", projectKey, "secrets"] as const,
  incidents: (projectKey: string) => ["project", projectKey, "incidents"] as const,
  observations: (projectKey: string) => ["project", projectKey, "observations"] as const,
  members: (projectKey: string) => ["project", projectKey, "members"] as const,
  audit: (projectKey: string) => ["project", projectKey, "audit"] as const,
};

function handleUnauthorized(client: QueryClient, error: unknown) {
  if (!(error instanceof ApiError) || error.status !== 401) return;
  client.setQueryData(queryKeys.session, null);
  void client.cancelQueries({ predicate: (query) => query.queryKey[0] !== "session" });
  client.removeQueries({ predicate: (query) => query.queryKey[0] !== "session" });
}

export function createAppQueryClient(): QueryClient {
  const clientRef: { current: QueryClient | null } = { current: null };
  const handleError = (error: unknown): void => {
    if (clientRef.current) handleUnauthorized(clientRef.current, error);
  };
  const client = new QueryClient({
    queryCache: new QueryCache({ onError: handleError }),
    mutationCache: new MutationCache({ onError: handleError }),
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: 15_000,
        refetchOnWindowFocus: false,
      },
      mutations: { retry: false },
    },
  });
  clientRef.current = client;
  return client;
}
