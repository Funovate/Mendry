import type { OverviewTask } from "./api";

export const stages = [
  { id: "queued", label: "Queued", short: "Queue", x: 0, y: 0 },
  { id: "preparing_context", label: "Preparing context", short: "Prepare", x: 290, y: 0 },
  { id: "diagnosing", label: "Diagnosing", short: "Diagnose", x: 580, y: 0 },
  { id: "planning", label: "Planning", short: "Plan", x: 870, y: 0 },
  { id: "patching", label: "Patching", short: "Patch", x: 1160, y: 0 },
  { id: "validating", label: "Validating", short: "Validate", x: 1450, y: 0 },
  { id: "publishing", label: "Publishing", short: "Publish", x: 1740, y: 0 },
  { id: "collecting_more_context", label: "Collecting more context", short: "More evidence", x: 580, y: 350 },
  { id: "diagnosis_ready_for_review", label: "Diagnosis ready for review", short: "Diagnosis ready", x: 870, y: 350 },
  { id: "completed_non_code", label: "Non-code conclusion", short: "Non-code conclusion", x: 290, y: 350 },
  { id: "awaiting_human_review", label: "Awaiting human review", short: "Review repair", x: 1740, y: 350 },
  { id: "blocked_manual_review", label: "Manual intervention", short: "Needs attention", x: 1160, y: 350 },
  { id: "failed", label: "Failed", short: "Failed", x: 1450, y: 700 },
  { id: "budget_exhausted", label: "Budget exhausted", short: "Budget exhausted", x: 1160, y: 700 },
  { id: "running", label: "Running · phase unspecified", short: "Running", x: 0, y: 350 },
] as const;
const terminalStates = new Set(["diagnosis_ready_for_review", "completed_non_code", "awaiting_human_review", "blocked_manual_review", "failed", "budget_exhausted"]);
export const isTerminal = (state: string) => terminalStates.has(state);
export const stateLabel = (state: string) => stages.find((stage) => stage.id === state)?.label ?? `Unknown stage · ${state}`;
export const number = (value: number) => new Intl.NumberFormat("en-US").format(value);
export function duration(seconds: number | null) {
  if (seconds === null) return "Not available";
  if (seconds < 60) return `${Math.max(0, Math.floor(seconds))}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
  return `${Math.floor(seconds / 86400)}d ${Math.floor(seconds % 86400 / 3600)}h`;
}
export function taskDuration(task: OverviewTask, now: number, phase = false) {
  const start = phase ? task.stateEnteredAt : task.startedAt;
  if (!start) return "Phase duration unknown";
  const end = task.endedAt ? Date.parse(task.endedAt) : now;
  return duration(Math.max(0, end - Date.parse(start)) / 1000);
}
export const dateTime = (value: string, timezone: string) => new Intl.DateTimeFormat("en-US", {
  timeZone: timezone, month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", timeZoneName: "short",
}).format(new Date(value));
