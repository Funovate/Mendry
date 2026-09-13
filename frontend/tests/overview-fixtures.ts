import type { Overview, OverviewTask } from "../src/features/overview/api";

export function overviewTask(overrides: Partial<OverviewTask> = {}): OverviewTask {
  return { runId: "run-1", incidentId: "INC-2048", title: "Validator locale fr is not registered", state: "diagnosing",
    attemptNumber: 1, generation: 1, startedAt: "2026-09-12T07:30:00Z", endedAt: null,
    stateEnteredAt: "2026-09-12T07:40:00Z", tokensIn: 1000, tokensOut: 200, model: "gpt-test",
    usageRecorded: true, retryable: false, latest: true, ...overrides };
}
export function overviewFixture(tasks: OverviewTask[] = [overviewTask()]): Overview {
  return {
    generatedAt: "2026-09-12T08:00:00Z", timezone: "UTC", range: "7d", collectionStartedAt: "2026-09-11T08:30:00Z",
    counts: { processed: 12, todayProcessed: 3, activeIncidents: tasks.length, activeTasks: tasks.length,
      successful: 9, failed: 2, budgetExhausted: 1, recovered: 4, waiting: 3 },
    tokens: { input: 100000, output: 20000, todayInput: 1000, todayOutput: 200, unrecordedTasks: 1 },
    stages: [...new Set(tasks.map((task) => task.state))].map((state) => ({ state,
      count: tasks.filter((task) => task.state === state).length, tasks: tasks.filter((task) => task.state === state).slice(0, 3) })),
    trend: [
      { start: "2026-09-10T00:00:00Z", end: "2026-09-11T00:00:00Z", input: 0, output: 0, covered: false, partial: false },
      { start: "2026-09-11T00:00:00Z", end: "2026-09-12T00:00:00Z", input: 3000, output: 500, covered: true, partial: true },
      { start: "2026-09-12T00:00:00Z", end: "2026-09-13T00:00:00Z", input: 1000, output: 200, covered: true, partial: false },
    ],
    attention: { since: "2026-09-06T00:00:00Z", sampleCount: 12, medianSeconds: 420, p95Seconds: 1800, budgetExhausted: 1,
      failures: [{ reason: "provider_timeout", count: 1 }, { reason: "budget_exhausted", count: 1 }], longest: tasks.slice(0, 5) },
  };
}
