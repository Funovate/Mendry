import { afterEach, describe, expect, it, vi } from "vitest";
import { notificationApi, ApiContractError } from "../src/api";
const channel = { id: "channel-1", name: "Ops", platform: "telegram", enabled: true, hasCredentials: true, createdAt: "2026-08-13T08:00:00Z", updatedAt: "2026-08-13T08:00:00Z" };
const envelope = (data: unknown, total?: number) => new Response(JSON.stringify({ code: "ok", message: "OK", data, meta: { requestId: "request-1", durationMs: 1, ...(total === undefined ? {} : { total }) } }), { status: 200 });
afterEach(() => vi.unstubAllGlobals());
describe("notification API contracts", () => {
  it("unwraps safe channels and strips unexpected credential fields", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope([{ ...channel, botToken: "should-not-be-available" }], 1)));
    await expect(notificationApi.listChannels("payments")).resolves.toEqual({ items: [channel], total: 1 });
  });
  it("creates a channel with credentials but omits them for metadata-only updates", async () => {
    const fetchMock = vi.fn().mockImplementation(async () => envelope(channel)); vi.stubGlobal("fetch", fetchMock);
    const input = { name: "Ops", platform: "telegram" as const, enabled: true, credentials: { botToken: "token", chatId: "123" } };
    await notificationApi.saveChannel("payments", input);
    expect(fetchMock).toHaveBeenLastCalledWith("/api/v1/projects/payments/notifications/channels", expect.objectContaining({ method: "POST", body: JSON.stringify(input), credentials: "include" }));
    const metadata = { name: "Renamed", platform: "telegram" as const, enabled: false };
    await notificationApi.saveChannel("payments", metadata, "channel-1");
    expect(fetchMock).toHaveBeenLastCalledWith("/api/v1/projects/payments/notifications/channels/channel-1", expect.objectContaining({ method: "PUT", body: JSON.stringify(metadata) }));
  });
  it("validates history state and requires server totals", async () => {
    const delivery = { id: "delivery-1", channelId: channel.id, channelName: channel.name, platform: channel.platform, kind: "result", incidentNumber: 2049, generation: 2, state: "failed", attempts: 12, lastError: "platform_delivery_failed", createdAt: channel.createdAt, nextAttemptAt: channel.createdAt, deliveredAt: null };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope([delivery], 1)));
    await expect(notificationApi.listDeliveries("payments")).resolves.toEqual({ items: [delivery], total: 1 });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope([{ ...delivery, state: "arbitrary" }], 1)));
    await expect(notificationApi.listDeliveries("payments")).rejects.toBeInstanceOf(ApiContractError);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(envelope([])));
    await expect(notificationApi.listDeliveries("payments")).rejects.toBeInstanceOf(ApiContractError);
  });
  it("tests channels and retries individual deliveries", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(envelope({ sent: true })).mockResolvedValueOnce(envelope({ queued: true })); vi.stubGlobal("fetch", fetchMock);
    await expect(notificationApi.testChannel("payments", "channel-1")).resolves.toEqual({ sent: true });
    await expect(notificationApi.retryDelivery("payments", "delivery-1")).resolves.toEqual({ queued: true });
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/projects/payments/notifications/channels/channel-1/test", expect.objectContaining({ method: "POST" }));
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/payments/notifications/deliveries/delivery-1/retry", expect.objectContaining({ method: "POST" }));
  });
  it("accepts empty delete responses", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(notificationApi.deleteChannel("payments", "channel-1")).resolves.toBeUndefined();
  });
});
