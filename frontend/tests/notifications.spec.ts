import { expect, test, type Page } from "@playwright/test";

const now = "2026-08-13T08:00:00Z";
const project = { id: "project-1", key: "payments", name: "Payments", description: "", version: 1, createdAt: now, updatedAt: now };
const channelId = "0198abcd-0000-7000-8000-000000000001";
const deliveryId = "0198abcd-0000-7000-8000-000000000002";

type Channel = { id: string; name: string; platform: string; enabled: boolean; hasCredentials: boolean; createdAt: string; updatedAt: string };

async function notificationFixture(page: Page) {
  let channels: Channel[] = [];
  let deliveryState = "failed";
  const writes: Array<{ method: string; path: string; body: Record<string, unknown> | null }> = [];
  await page.route("**/api/v1/**", async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const json = (data: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ code: "ok", message: "OK", data, meta: { requestId: "notifications-e2e", durationMs: 1, ...(Array.isArray(data) ? { total: data.length } : {}) } }) });
    if (method !== "GET") writes.push({ method, path, body: request.postData() ? request.postDataJSON() : null });
    if (path === "/api/v1/auth/me") return json({ id: "admin", username: "admin" });
    if (path === "/api/v1/projects") return json([project]);
    if (path === "/api/v1/projects/payments") return json(project);
    if (path.endsWith("/notifications/channels") && method === "GET") return json(channels);
    if (path.endsWith("/notifications/channels") && method === "POST") {
      const input = request.postDataJSON();
      const channel = { id: channelId, name: input.name, platform: input.platform, enabled: input.enabled, hasCredentials: true, createdAt: now, updatedAt: now };
      channels = [...channels, channel];
      return json(channel, 201);
    }
    if (path.endsWith(`/channels/${channelId}`) && method === "PUT") {
      const input = request.postDataJSON();
      channels = channels.map(channel => ({ ...channel, name: input.name, enabled: input.enabled }));
      return json(channels[0]);
    }
    if (path.endsWith(`/channels/${channelId}`) && method === "DELETE") {
      channels = [];
      return route.fulfill({ status: 204 });
    }
    if (path.endsWith(`/channels/${channelId}/test`)) return json({ sent: true });
    if (path.endsWith("/notifications/deliveries")) return json([
      { id: deliveryId, channelId, channelName: "Ops TG", platform: "telegram", kind: "trigger", incidentNumber: 2048, generation: 1, state: deliveryState, attempts: deliveryState === "failed" ? 12 : 0, lastError: deliveryState === "failed" ? "platform_delivery_failed" : "", createdAt: now, nextAttemptAt: now, deliveredAt: null },
      { id: "cancelled-delivery", channelId, channelName: "Old channel", platform: "feishu", kind: "result", incidentNumber: 2049, generation: 1, state: "cancelled", attempts: 0, lastError: "", createdAt: now, nextAttemptAt: now, deliveredAt: null },
    ]);
    if (path.endsWith(`/deliveries/${deliveryId}/retry`)) { deliveryState = "pending"; return json({ queued: true }); }
    return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: { code: "not_found", message: "Not found." } }) });
  });
  return writes;
}

test("notification channels support create, test, enable, safe edit, and delete", async ({ page }) => {
  const writes = await notificationFixture(page);
  await page.goto("/projects/payments/configuration/notifications");
  await expect(page.getByRole("heading", { name: "No channels" })).toBeVisible();
  await page.getByRole("button", { name: "Add channel", exact: true }).click();
  await page.getByLabel("Name", { exact: true }).fill("Ops TG");
  await page.getByLabel("Bot token").fill("123456:abcdefghijklmnop");
  await page.getByLabel("Chat ID").fill("-100123456789");
  await page.getByRole("button", { name: "Save channel" }).click();
  await expect(page.getByRole("cell", { name: "Ops TG", exact: true })).toBeVisible();
  expect(writes[0].body).toMatchObject({ credentials: { botToken: "123456:abcdefghijklmnop", chatId: "-100123456789" } });
  await page.getByRole("button", { name: "Test Ops TG" }).click();
  await expect(page.getByRole("status")).toHaveText("Test message sent.");
  await page.getByRole("checkbox", { name: "Enable Ops TG" }).click();
  await expect(page.getByRole("checkbox", { name: "Enable Ops TG" })).not.toBeChecked();
  await expect(page.getByRole("status")).toHaveText("Channel updated.");
  await page.getByRole("button", { name: "Edit Ops TG" }).click();
  await expect(page.getByRole("combobox")).toBeDisabled();
  await expect(page.getByLabel("Bot token")).toHaveCount(0);
  await page.getByLabel("Name", { exact: true }).fill("Primary TG");
  await page.getByRole("button", { name: "Save channel" }).click();
  await expect(page.getByRole("cell", { name: "Primary TG", exact: true })).toBeVisible();
  const updates = writes.filter(write => write.method === "PUT");
  expect(updates.every(write => !Object.hasOwn(write.body ?? {}, "credentials"))).toBe(true);
  page.once("dialog", dialog => dialog.accept());
  await page.getByRole("button", { name: "Delete Primary TG" }).click();
  await expect(page.getByRole("heading", { name: "No channels" })).toBeVisible();
});

test("delivery history displays cancellation and retries only failed records", async ({ page }) => {
  const writes = await notificationFixture(page);
  await page.goto("/projects/payments/configuration/notifications");
  await page.getByRole("tab", { name: "Delivery history" }).click();
  await expect(page.getByRole("cell", { name: "cancelled", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "#2048" })).toHaveAttribute("href", "/projects/payments/incidents/INC-2048");
  await page.getByRole("button", { name: `Retry delivery ${deliveryId}` }).click();
  await expect(page.getByRole("cell", { name: "pending", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: `Retry delivery ${deliveryId}` })).toHaveCount(0);
  expect(writes).toContainEqual({ method: "POST", path: `/api/v1/projects/payments/notifications/deliveries/${deliveryId}/retry`, body: null });
});

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`notification editor fits ${viewport.width}px viewport`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    await notificationFixture(page);
    await page.goto("/projects/payments/configuration/notifications");
    const heading = page.getByRole("heading", { name: "Notifications", exact: true });
    await expect(heading).toBeInViewport();
    await page.getByRole("button", { name: "Add channel", exact: true }).click();
    await page.getByRole("combobox").selectOption("feishu");
    await expect(page.getByLabel("Webhook URL")).toBeVisible();
    await expect(page.getByLabel("Signing secret (optional)")).toBeVisible();
    const overflow = await page.locator(".notification-view").evaluate(element => element.scrollWidth > element.clientWidth);
    expect(overflow).toBe(false);
    await page.screenshot({ path: testInfo.outputPath(`notifications-${viewport.width}.png`), fullPage: true });
    await page.getByRole("combobox").selectOption("wecom");
    await expect(page.getByLabel("Signing secret (optional)")).toHaveCount(0);
    await expect(page.getByLabel("Webhook URL")).toHaveAttribute("type", "password");
  });
}
