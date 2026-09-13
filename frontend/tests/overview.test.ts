import { describe, expect, it, vi } from "vitest";
import { ApiContractError, ApiError } from "../src/api";
import { getOverview, getOverviewTasks, overviewKeys, overviewSchema } from "../src/features/overview/api";
import { taskDuration } from "../src/features/overview/format";
import { overviewFixture, overviewTask } from "./overview-fixtures";

const envelope = (data: unknown, total?: number) => ({ code: "ok", message: "OK", data, meta: { requestId: "test", durationMs: 1, ...(total === undefined ? {} : { total }) } });
describe("overview API", () => {
  it("validates all metrics and preserves missing historical coverage", () => {
    const result = overviewSchema.parse(overviewFixture());
    expect(result.trend[0].covered).toBe(false);
    expect(overviewSchema.safeParse({ ...result, tokens: { ...result.tokens, input: -1 } }).success).toBe(false);
  });
  it("passes cancellation and project/timezone parameters", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(envelope(overviewFixture()))));
    vi.stubGlobal("fetch", fetcher);
    const controller = new AbortController();
    await getOverview("project/key", "Asia/Shanghai", "7d", controller.signal);
    expect(fetcher).toHaveBeenCalledWith("/api/v1/projects/project%2Fkey/overview?timezone=Asia%2FShanghai&range=7d", { credentials: "include", signal: controller.signal });
  });
  it("keeps total when requesting an empty task page", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(envelope([], 45)))));
    const result = await getOverviewTasks("project", { state: "", scope: "all", sort: "recent", page: 8 });
    expect(result).toEqual({ items: [], total: 45 });
  });
  it("uses shared unauthorized errors and rejects malformed success", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}", { status: 401 })));
    await expect(getOverview("p", "UTC", "today")).rejects.toBeInstanceOf(ApiError);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("{}")));
    await expect(getOverview("p", "UTC", "today")).rejects.toBeInstanceOf(ApiContractError);
  });
  it("separates project and filter caches", () => {
    expect(overviewKeys.snapshot("a", "UTC", "7d")).not.toEqual(overviewKeys.snapshot("b", "UTC", "7d"));
    expect(overviewKeys.snapshot("a", "UTC", "7d")).not.toEqual(overviewKeys.snapshot("a", "UTC", "30d"));
  });
});
it("does not infer a phase clock for old attempts or keep terminal clocks running", () => {
  expect(taskDuration(overviewTask({ stateEnteredAt: null }), Date.now(), true)).toBe("Phase duration unknown");
  expect(taskDuration(overviewTask({ endedAt: "2026-09-12T07:40:00Z" }), Date.now())).toBe("10m 0s");
});
