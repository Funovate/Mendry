import { expect, test, type Page } from "@playwright/test";
import { overviewFixture, overviewTask } from "./overview-fixtures";

async function mockOverview(page: Page) {
  const tasks = Array.from({ length: 24 }, (_, i) => overviewTask({ runId: `run-${i}`, incidentId: `INC-${2048 + i}`, title: `Incident task ${i}`, state: i === 0 ? "future_phase" : "diagnosing" }));
  let phase = "diagnosing", failed = false, unauthorized = false;
  const requests: URL[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url()); requests.push(url);
    const json = (data: unknown, total?: number) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: "ok", message: "OK", data, meta: { requestId: "test", durationMs: 1, ...(total === undefined ? {} : { total }) } }) });
    const project = { id: "project-id", key: "demo", name: "Demo project", description: "", version: 1, createdAt: "2026-09-12T00:00:00Z", updatedAt: "2026-09-12T00:00:00Z" };
    if (url.pathname.endsWith("/auth/me")) return json({ id: "user", username: "admin" });
    if (url.pathname === "/api/v1/projects") return json([project], 1);
    if (url.pathname === "/api/v1/projects/demo") return json(project);
    if (url.pathname.endsWith("/overview") || url.pathname.endsWith("/overview/tasks")) {
      if (unauthorized) return route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: { code: "unauthenticated", message: "Session expired." } }) });
      if (failed) return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "unavailable", message: "Overview temporarily unavailable." } }) });
      const current = tasks.map((task, i) => ({ ...task, state: i === 0 ? "future_phase" : phase }));
      if (url.pathname.endsWith("/tasks")) {
        const state = url.searchParams.get("state");
        const filtered = current.filter((task) => !state || task.state === state);
        const offset = (Number(url.searchParams.get("page") || 1) - 1) * 20;
        return json(filtered.slice(offset, offset + 20), filtered.length);
      }
      const snapshot = overviewFixture(current);
      snapshot.timezone = url.searchParams.get("timezone") || "UTC";
      snapshot.range = (url.searchParams.get("range") || "7d") as typeof snapshot.range;
      return json(snapshot);
    }
    return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "Not found" } }) });
  });
  return { requests, changePhase: () => { phase = "planning"; }, fail: () => { failed = true; }, expire: () => { unauthorized = true; } };
}

test("project entry shows overview, bounded flow cards, filtering, pagination and task summary", async ({ page }) => {
  await page.setViewportSize({ width: 1500, height: 1100 });
  const state = await mockOverview(page);
  await page.goto("/");
  await expect(page).toHaveURL(/\/projects\/demo\/overview$/);
  await expect(page.getByRole("heading", { level: 1, name: "Overview" })).toBeVisible();
  await expect(page.locator(".overview-stats article").first()).toContainText("12");
  await expect(page.locator('.react-flow__node[data-id="diagnosing"] .overview-task-card')).toHaveCount(3);
  await expect(page.getByRole("button", { name: "Filter Unknown stage · future_phase tasks" })).toBeAttached();
  await page.getByRole("button", { name: "Filter Diagnosing tasks" }).click();
  await expect(page.getByLabel("Task stage")).toHaveValue("diagnosing");
  await expect(page.locator(".overview-pagination")).toContainText("23 attempts");
  await page.getByLabel("Next task page").click();
  await expect(page.locator(".overview-task-table tbody tr")).toHaveCount(3);
  await page.locator(".overview-task-table tbody button").first().click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("dialog").getByRole("link", { name: /Open incident/ })).toHaveAttribute("href", /\/incidents\/INC-/);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByLabel("Overview time range").selectOption("30d");
  await expect.poll(() => state.requests.some((request) => request.searchParams.get("range") === "30d")).toBeTruthy();
  await page.getByText("View consumption data", { exact: true }).click();
  await expect(page.locator(".overview-trend-data")).toContainText("No data");
  await page.screenshot({ path: "test-results/overview-desktop.png", fullPage: true });
});

test("polling moves tasks and a refresh failure preserves the last snapshot", async ({ page }) => {
  const state = await mockOverview(page);
  await page.goto("/projects/demo/overview");
  await expect(page.locator('.react-flow__node[data-id="diagnosing"] .overview-task-card')).toHaveCount(3);
  state.changePhase();
  await expect(page.locator('.react-flow__node[data-id="planning"] .overview-task-card')).toHaveCount(3, { timeout: 10000 });
  await expect(page.locator('.react-flow__node[data-id="diagnosing"] .overview-task-card')).toHaveCount(0);
  state.fail();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByText(/Refresh failed. Showing the last successful snapshot/)).toBeVisible();
  await expect(page.locator(".overview-stats article").first()).toContainText("12");
});

test("expired overview session returns to login", async ({ page }) => {
  const state = await mockOverview(page);
  await page.goto("/projects/demo/overview");
  await expect(page.getByRole("heading", { level: 1, name: "Overview" })).toBeVisible();
  state.expire();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
});

test("mobile overview stays within the viewport and exposes an accessible stage filter", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await mockOverview(page);
  await page.goto("/projects/demo/overview");
  await expect(page.getByRole("heading", { level: 1, name: "Overview" })).toBeVisible();
  await expect(page.locator(".overview-stats")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.getByLabel("Task stage").selectOption("future_phase");
  await expect(page.locator(".overview-task-table tbody tr")).toHaveCount(1);
  await page.screenshot({ path: "test-results/overview-mobile.png", fullPage: true });
});
