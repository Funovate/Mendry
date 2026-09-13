import { z } from "zod";
import { ApiContractError, ApiError } from "../../api";

const count = z.number().int().nonnegative();
const timestamp = z.string().datetime({ offset: true });
export const taskSchema = z.object({
  runId: z.string(), incidentId: z.string(), title: z.string(), state: z.string(),
  attemptNumber: count, generation: count, startedAt: timestamp, endedAt: timestamp.nullable(),
  stateEnteredAt: timestamp.nullable(), tokensIn: count, tokensOut: count, model: z.string(),
  usageRecorded: z.boolean(), retryable: z.boolean(), latest: z.boolean(),
});
export const overviewSchema = z.object({
  generatedAt: timestamp, timezone: z.string(), range: z.enum(["today", "7d", "30d"]), collectionStartedAt: timestamp,
  counts: z.object({ processed: count, todayProcessed: count, activeIncidents: count, activeTasks: count,
    successful: count, failed: count, budgetExhausted: count, recovered: count, waiting: count }),
  tokens: z.object({ input: count, output: count, todayInput: count, todayOutput: count, unrecordedTasks: count }),
  stages: z.array(z.object({ state: z.string(), count, tasks: z.array(taskSchema).max(3) })),
  trend: z.array(z.object({ start: timestamp, end: timestamp, input: count, output: count, covered: z.boolean(), partial: z.boolean() })),
  attention: z.object({ sampleCount: count, medianSeconds: z.number().nonnegative().nullable(),
    p95Seconds: z.number().nonnegative().nullable(), budgetExhausted: count,
    failures: z.array(z.object({ reason: z.string(), count })), longest: z.array(taskSchema).max(5), since: timestamp }),
});
export type Overview = z.infer<typeof overviewSchema>;
export type OverviewTask = z.infer<typeof taskSchema>;
export type Period = Overview["range"];
export type TaskScope = "all" | "flow" | "attention" | "failed" | "budget";
export type TaskFilters = { state: string; scope: TaskScope; sort: "recent" | "tokens"; page: number };

const metaSchema = z.object({ requestId: z.string(), durationMs: count });
async function get<T>(path: string, schema: z.ZodType<T>, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { credentials: "include", signal });
  if (!response.ok) {
    const errorSchema = z.object({ error: z.object({ code: z.string(), message: z.string(), requestId: z.string().optional() }) });
    const body: unknown = await response.json().catch(() => null);
    const result = errorSchema.safeParse(body);
    throw new ApiError(response.status, result.success ? result.data.error.code : "request_failed",
      result.success ? result.data.error.message : `Request failed with status ${response.status}.`,
      result.success ? result.data.error.requestId : undefined);
  }
  let body: unknown;
  try { body = await response.json(); } catch (cause) { throw new ApiContractError(path, "Invalid overview response.", cause); }
  const result = schema.safeParse(body);
  if (!result.success) throw new ApiContractError(path, "Invalid overview response.", result.error);
  return result.data;
}
export const overviewKeys = {
  snapshot: (key: string, timezone: string, range: Period) => ["overview", key, "snapshot", timezone, range] as const,
  tasks: (key: string, filters: TaskFilters) => ["overview", key, "tasks", filters] as const,
};
export async function getOverview(key: string, timezone: string, range: Period, signal?: AbortSignal) {
  const envelope = z.object({ code: z.literal("ok"), message: z.literal("OK"), data: overviewSchema, meta: metaSchema });
  const query = new URLSearchParams({ timezone, range });
  return (await get(`/api/v1/projects/${encodeURIComponent(key)}/overview?${query}`, envelope, signal)).data;
}
export async function getOverviewTasks(key: string, filters: TaskFilters, signal?: AbortSignal) {
  const query = new URLSearchParams({ ...filters, page: String(filters.page), pageSize: "20" });
  const envelope = z.object({ code: z.literal("ok"), message: z.literal("OK"), data: z.array(taskSchema), meta: metaSchema.extend({ total: count }) });
  const result = await get(`/api/v1/projects/${encodeURIComponent(key)}/overview/tasks?${query}`, envelope, signal);
  return { items: result.data, total: result.meta.total };
}
