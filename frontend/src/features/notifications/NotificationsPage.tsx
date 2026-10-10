import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Bell, Check, Key, Pencil, Plus, Radio, RefreshCw, Save, Send, Trash2, X } from "lucide-react";
import { useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { messageFromError, notificationApi, type NotificationChannel, type NotificationChannelInput } from "../../api";
import { useCurrentProject } from "../../app/context";
import { ErrorNotice, LoadingState, PageError } from "../../shared/ui";
import "./notifications.css";

const platforms = {
  telegram: "Telegram",
  feishu: "Feishu",
  wecom: "WeCom",
  slack: "Slack",
  discord: "Discord",
  whatsapp: "WhatsApp",
} as const;

const platformDetails: Record<keyof typeof platforms, { namePlaceholder: string; hint: string; webhookPlaceholder?: string }> = {
  telegram: { namePlaceholder: "e.g. SRE Telegram Channel", hint: "Telegram Bot API: requires Bot Token and target Chat ID." },
  feishu: {
    namePlaceholder: "e.g. On-Call Feishu Group",
    hint: "Feishu Group Bot: incoming webhook URL with optional signing secret.",
    webhookPlaceholder: "https://open.feishu.cn/open-apis/bot/v2/hook/...",
  },
  wecom: {
    namePlaceholder: "e.g. WeCom Incident Room",
    hint: "WeCom Group Robot: incoming webhook URL for WeChat Work.",
    webhookPlaceholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...",
  },
  slack: {
    namePlaceholder: "e.g. #incidents Slack",
    hint: "Slack Incoming Webhook: webhook URL from your Slack app.",
    webhookPlaceholder: "https://hooks.slack.com/services/T.../B.../...",
  },
  discord: {
    namePlaceholder: "e.g. Discord On-Call",
    hint: "Discord Webhook: webhook URL from the channel's Integrations settings.",
    webhookPlaceholder: "https://discord.com/api/webhooks/.../...",
  },
  whatsapp: {
    namePlaceholder: "e.g. On-Call WhatsApp",
    hint: "WhatsApp Cloud API: requires access token, phone number ID, and recipient number.",
  },
};

function PlatformIcon({ platform, size = 15 }: { platform: keyof typeof platforms; size?: number }) {
  if (platform === "telegram") {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="platform-icon-svg">
        <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 6.8c-.15 1.58-.8 5.42-1.13 7.19-.14.75-.42 1-.68 1.03-.58.05-1.02-.38-1.58-.75-.88-.58-1.38-.94-2.23-1.5-.99-.65-.35-1.01.22-1.59.15-.15 2.71-2.48 2.76-2.69a.2.2 0 00-.05-.18c-.06-.05-.14-.03-.21-.02-.09.02-1.49.95-4.22 2.79-.4.27-.76.41-1.08.4-.36-.01-1.04-.2-1.55-.37-.63-.2-1.12-.31-1.08-.66.02-.18.27-.36.74-.55 2.92-1.27 4.86-2.11 5.83-2.51 2.78-1.16 3.35-1.36 3.73-1.36.08 0 .27.02.39.12.1.08.13.19.14.27-.01.06.01.24 0 .36z" />
      </svg>
    );
  }
  if (platform === "slack") {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="platform-icon-svg">
        <path d="M5.04 15.17a2.53 2.53 0 11-2.52-2.53h2.52v2.53zm1.27 0a2.53 2.53 0 015.05 0v6.31a2.53 2.53 0 11-5.05 0v-6.31zM8.83 5.04a2.53 2.53 0 112.53-2.52v2.52H8.83zm0 1.27a2.53 2.53 0 010 5.05H2.52a2.53 2.53 0 010-5.05h6.31zm10.13 2.52a2.53 2.53 0 112.52 2.53h-2.52V8.83zm-1.27 0a2.53 2.53 0 01-5.05 0V2.52a2.53 2.53 0 115.05 0v6.31zm-2.52 10.13a2.53 2.53 0 11-2.53 2.52v-2.52h2.53zm0-1.27a2.53 2.53 0 010-5.05h6.31a2.53 2.53 0 010 5.05h-6.31z" />
      </svg>
    );
  }
  if (platform === "discord") {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="platform-icon-svg">
        <path d="M20.32 4.37A19.8 19.8 0 0015.39 2.8a.07.07 0 00-.08.04c-.21.38-.45.87-.61 1.25a18.27 18.27 0 00-5.49 0 12.6 12.6 0 00-.62-1.25.08.08 0 00-.08-.04 19.74 19.74 0 00-4.92 1.53.07.07 0 00-.03.03C.53 9.05-.32 13.58.1 18.06a.08.08 0 00.03.06 19.9 19.9 0 006 3.03.08.08 0 00.08-.03c.46-.63.87-1.3 1.23-2a.08.08 0 00-.04-.11 13.1 13.1 0 01-1.87-.89.08.08 0 01-.01-.13l.37-.29a.07.07 0 01.08-.01c3.93 1.79 8.18 1.79 12.06 0a.07.07 0 01.08.01l.37.29a.08.08 0 01-.01.13c-.6.35-1.22.65-1.87.89a.08.08 0 00-.04.11c.36.7.78 1.36 1.23 2a.08.08 0 00.08.03 19.84 19.84 0 006.01-3.03.08.08 0 00.03-.05c.5-5.18-.84-9.67-3.55-13.66a.06.06 0 00-.03-.03zM8.02 15.33c-1.18 0-2.16-1.09-2.16-2.42s.96-2.42 2.16-2.42c1.21 0 2.18 1.1 2.16 2.42 0 1.33-.96 2.42-2.16 2.42zm7.97 0c-1.18 0-2.16-1.09-2.16-2.42s.96-2.42 2.16-2.42c1.21 0 2.18 1.1 2.16 2.42 0 1.33-.95 2.42-2.16 2.42z" />
      </svg>
    );
  }
  if (platform === "whatsapp") {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="platform-icon-svg">
        <path d="M17.47 14.38c-.3-.15-1.76-.87-2.03-.97-.27-.1-.47-.15-.67.15-.2.3-.77.97-.94 1.16-.17.2-.35.22-.64.07-.3-.15-1.26-.46-2.39-1.47-.88-.79-1.48-1.76-1.65-2.06-.17-.3-.02-.46.13-.6.13-.14.3-.35.45-.52.15-.17.2-.3.3-.5.1-.2.05-.37-.03-.52-.07-.15-.67-1.62-.92-2.21-.24-.58-.49-.5-.67-.51h-.57c-.2 0-.52.07-.79.37-.27.3-1.04 1.02-1.04 2.48s1.07 2.88 1.21 3.07c.15.2 2.1 3.2 5.08 4.49.71.31 1.26.49 1.69.63.71.22 1.36.19 1.87.12.57-.09 1.76-.72 2.01-1.41.25-.7.25-1.29.17-1.41-.07-.13-.27-.2-.57-.35zM12.05 21.5h-.01a9.87 9.87 0 01-5.03-1.38l-.36-.21-3.74.98 1-3.65-.24-.37a9.86 9.86 0 01-1.51-5.26c0-5.45 4.44-9.88 9.89-9.88a9.82 9.82 0 016.99 2.9 9.82 9.82 0 012.89 6.99c0 5.45-4.44 9.88-9.88 9.88zm8.41-18.3A11.81 11.81 0 0012.05 0C5.5 0 .16 5.34.16 11.89c0 2.1.55 4.14 1.59 5.95L.06 24l6.3-1.65a11.88 11.88 0 005.68 1.45h.01c6.55 0 11.89-5.34 11.89-11.89a11.82 11.82 0 00-3.48-8.41z" />
      </svg>
    );
  }
  if (platform === "feishu") {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="platform-icon-svg">
        <path d="M12.5 3L3 13.5l4.5 4.5L17 8.5V3h-4.5zm1 2h2v2L8.5 14l-2-2L13.5 5zM8 19.5l3-3 1.5 1.5-3 3H8v-1.5zm8-8l3-3 1.5 1.5-3 3H16v-1.5z" />
      </svg>
    );
  }
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className="platform-icon-svg">
      <path d="M8.5 4C4.36 4 1 6.91 1 10.5c0 2.02 1.05 3.83 2.7 5.03L3 19l3.82-1.27c.54.18 1.1.27 1.68.27.31 0 .61-.03.91-.07-.2-.61-.31-1.26-.31-1.93 0-3.59 3.36-6.5 7.5-6.5.47 0 .93.04 1.38.12C17.25 6.32 13.25 4 8.5 4zm6.5 7c-3.59 0-6.5 2.46-6.5 5.5S11.41 22 15 22c.5 0 .98-.07 1.44-.21L19.5 23l-.62-2.48C19.78 19.53 20.5 18.11 20.5 16.5c0-3.04-2.91-5.5-6.5-5.5z" />
    </svg>
  );
}

export function NotificationsPage() {
  const project = useCurrentProject();
  return <NotificationSettings key={project.key} projectKey={project.key} />;
}

function NotificationSettings({ projectKey }: { projectKey: string }) {
  const client = useQueryClient();
  const [tab, setTab] = useState<"channels" | "deliveries">("channels");
  const [editor, setEditor] = useState<NotificationChannel | "new" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const channelKey = ["notification-channels", projectKey];
  const deliveryKey = ["notification-deliveries", projectKey];
  const channels = useQuery({
    queryKey: channelKey,
    queryFn: ({ signal }) => notificationApi.listChannels(projectKey, signal),
  });
  const deliveries = useQuery({
    queryKey: deliveryKey,
    queryFn: ({ signal }) => notificationApi.listDeliveries(projectKey, signal),
    enabled: tab === "deliveries",
    refetchInterval: tab === "deliveries" ? 10000 : false,
  });

  const action = async (work: () => Promise<unknown>, success: string) => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await work();
      setNotice(success);
      await client.invalidateQueries({ queryKey: channelKey });
      await client.invalidateQueries({ queryKey: deliveryKey });
    } catch (err) {
      setError(messageFromError(err));
    } finally {
      setBusy(false);
    }
  };

  const channelItems = channels.data?.items ?? [];
  const activeChannelsCount = channelItems.filter(c => c.enabled).length;

  return (
    <section className="notification-view settings-view">
      <div className="view-header">
        <div className="view-header-main">
          <Link className="notification-back" to={`/projects/${encodeURIComponent(projectKey)}/configuration`}>
            <ArrowLeft size={14} />
            Configuration
          </Link>
          <h1>Notifications</h1>
          <p className="view-header-subtitle">
            Configure outbound alert channels, chat bot webhooks, and inspect delivery execution history.
          </p>
        </div>
        <div className="view-header-actions">
          <button
            className="primary-button"
            type="button"
            disabled={busy}
            onClick={() => {
              setEditor("new");
              setError("");
              setNotice("");
            }}
          >
            <Plus size={16} />
            Add channel
          </button>
        </div>
      </div>

      {/* Top Overview Metrics Strip */}
      <div className="notification-overview-strip">
        <div className="notification-overview-item">
          <div className="notification-overview-icon">
            <Bell size={18} />
          </div>
          <div className="notification-overview-meta">
            <label>Channels</label>
            <strong>
              {channelItems.length} {channelItems.length === 1 ? "Channel" : "Channels"}
            </strong>
            <small>
              {activeChannelsCount} active · {channelItems.length - activeChannelsCount} disabled
            </small>
          </div>
        </div>

        <div className="notification-overview-item">
          <div className="notification-overview-icon">
            <Send size={18} />
          </div>
          <div className="notification-overview-meta">
            <label>Delivery Status</label>
            <strong>
              {deliveries.data ? `${deliveries.data.items.length} Logged` : "Auto-dispatching"}
            </strong>
            <small>Real-time incident alert routing</small>
          </div>
        </div>

        <div className="notification-overview-item">
          <div className="notification-overview-icon">
            <Radio size={18} />
          </div>
          <div className="notification-overview-meta">
            <label>Supported Platforms</label>
            <strong>{Object.values(platforms).join(" · ")}</strong>
            <small>Bot tokens & incoming webhooks</small>
          </div>
        </div>
      </div>

      {/* Tabs */}
      <div className="notification-tabs-container">
        <div className="notification-tabs" role="tablist" aria-label="Notification views">
          <button
            type="button"
            role="tab"
            aria-label="Channels"
            aria-selected={tab === "channels"}
            className={tab === "channels" ? "active" : ""}
            onClick={() => setTab("channels")}
          >
            <Bell size={15} />
            Channels
            {channels.data && <span className="tab-badge">{channelItems.length}</span>}
          </button>
          <button
            type="button"
            role="tab"
            aria-label="Delivery history"
            aria-selected={tab === "deliveries"}
            className={tab === "deliveries" ? "active" : ""}
            onClick={() => setTab("deliveries")}
          >
            <Send size={15} />
            Delivery history
            {deliveries.data && <span className="tab-badge">{deliveries.data.items.length}</span>}
          </button>
        </div>
      </div>

      {error && <ErrorNotice message={error} />}
      {notice && (
        <div className="notification-notice" role="status">
          <Check size={16} />
          {notice}
        </div>
      )}

      {editor && (
        <ChannelEditor
          key={typeof editor === "string" ? editor : editor.id}
          channel={editor === "new" ? undefined : editor}
          busy={busy}
          onClose={() => setEditor(null)}
          onSave={input =>
            void action(async () => {
              await notificationApi.saveChannel(projectKey, input, editor === "new" ? undefined : editor.id);
              setEditor(null);
            }, "Channel saved.")
          }
        />
      )}

      {tab === "channels" ? (
        channels.isPending ? (
          <LoadingState label="Loading channels" />
        ) : channels.isError ? (
          <PageError message={messageFromError(channels.error)} onRetry={() => void channels.refetch()} />
        ) : channelItems.length === 0 ? (
          <div className="notification-empty">
            <div className="notification-empty-icon">
              <Bell size={26} />
            </div>
            <h2>No channels</h2>
            <p>Connect your team's chat tools to receive notifications when incidents are detected and remediated.</p>
            <button
              className="primary-button"
              type="button"
              disabled={busy}
              onClick={() => {
                setEditor("new");
                setError("");
                setNotice("");
              }}
            >
              <Plus size={15} />
              Create channel
            </button>
          </div>
        ) : (
          <div className="table-wrap notification-table-scroll">
            <table className="notification-table">
              <thead>
                <tr>
                  <th>Channel</th>
                  <th>Platform</th>
                  <th>Enabled</th>
                  <th>Credentials</th>
                  <th className="th-actions">Actions</th>
                </tr>
              </thead>
              <tbody>
                {channelItems.map(channel => (
                  <tr key={channel.id}>
                    <td>
                      <span className="channel-name-cell">{channel.name}</span>
                    </td>
                    <td>
                      <span className={`platform-badge platform-${channel.platform}`}>
                        <PlatformIcon platform={channel.platform} size={14} />
                        {platforms[channel.platform]}
                      </span>
                    </td>
                    <td>
                      <label className="channel-toggle-wrapper">
                        <input
                          type="checkbox"
                          className="channel-checkbox"
                          aria-label={`Enable ${channel.name}`}
                          checked={channel.enabled}
                          disabled={busy}
                          onChange={e =>
                            void action(
                              () =>
                                notificationApi.saveChannel(
                                  projectKey,
                                  { name: channel.name, platform: channel.platform, enabled: e.target.checked },
                                  channel.id,
                                ),
                              "Channel updated.",
                            )
                          }
                        />
                        <span className={`channel-status-label ${channel.enabled ? "active" : "disabled"}`}>
                          {channel.enabled ? "Enabled" : "Disabled"}
                        </span>
                      </label>
                    </td>
                    <td>
                      {channel.hasCredentials ? (
                        <span className="status-pill open">
                          <Check size={11} />
                          Configured
                        </span>
                      ) : (
                        <span className="status-pill closed">Missing</span>
                      )}
                    </td>
                    <td className="td-actions">
                      <div className="notification-actions">
                        <button
                          className="icon-button"
                          type="button"
                          title={`Test ${channel.name}`}
                          aria-label={`Test ${channel.name}`}
                          disabled={busy}
                          onClick={() => void action(() => notificationApi.testChannel(projectKey, channel.id), "Test message sent.")}
                        >
                          <Send size={15} />
                        </button>
                        <button
                          className="icon-button"
                          type="button"
                          title={`Edit ${channel.name}`}
                          aria-label={`Edit ${channel.name}`}
                          disabled={busy}
                          onClick={() => {
                            setEditor(channel);
                            setError("");
                            setNotice("");
                          }}
                        >
                          <Pencil size={15} />
                        </button>
                        <button
                          className="icon-button delete-button"
                          type="button"
                          title={`Delete ${channel.name}`}
                          aria-label={`Delete ${channel.name}`}
                          disabled={busy}
                          onClick={() => {
                            if (window.confirm(`Delete channel "${channel.name}"?`)) {
                              void action(() => notificationApi.deleteChannel(projectKey, channel.id), "Channel deleted.");
                            }
                          }}
                        >
                          <Trash2 size={15} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      ) : deliveries.isPending ? (
        <LoadingState label="Loading delivery history" />
      ) : deliveries.isError ? (
        <PageError message={messageFromError(deliveries.error)} onRetry={() => void deliveries.refetch()} />
      ) : (
        <>
          <div className="notification-history-heading">
            <div className="history-heading-text">
              <h2>Recent deliveries</h2>
              <p>Delivery attempts and status for incident alerts and AI analysis reports.</p>
            </div>
            <button
              className="icon-button"
              title="Refresh deliveries"
              aria-label="Refresh deliveries"
              type="button"
              disabled={deliveries.isFetching}
              onClick={() => void deliveries.refetch()}
            >
              <RefreshCw size={15} className={deliveries.isFetching ? "spin" : ""} />
            </button>
          </div>
          {deliveries.data.items.length === 0 ? (
            <div className="notification-empty">
              <div className="notification-empty-icon">
                <Send size={26} />
              </div>
              <h2>No deliveries</h2>
              <p>No outbound notifications sent yet. When incidents occur, delivery logs will appear here.</p>
            </div>
          ) : (
            <div className="table-wrap notification-table-scroll">
              <table className="notification-table">
                <thead>
                  <tr>
                    <th>Incident</th>
                    <th>Event</th>
                    <th>Channel</th>
                    <th>Status</th>
                    <th>Attempts</th>
                    <th>Created</th>
                    <th>Error</th>
                    <th className="th-actions">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {deliveries.data.items.map(delivery => (
                    <tr key={delivery.id}>
                      <td>
                        <div className="delivery-incident-group">
                          <Link to={`/projects/${encodeURIComponent(projectKey)}/incidents/INC-${delivery.incidentNumber}`}>
                            #{delivery.incidentNumber}
                          </Link>
                          <small>Lifecycle {delivery.generation}</small>
                        </div>
                      </td>
                      <td>
                        <span className={`event-kind-tag kind-${delivery.kind}`}>
                          {delivery.kind === "trigger" ? "New incident" : "AI result"}
                        </span>
                      </td>
                      <td>
                        <strong className="delivery-channel-name">{delivery.channelName}</strong>
                      </td>
                      <td>
                        <span className={`notification-state ${delivery.state}`}>
                          <span className="state-dot" />
                          {delivery.state}
                        </span>
                      </td>
                      <td>
                        <span className="delivery-attempts-badge">{delivery.attempts}</span>
                      </td>
                      <td className="delivery-date-cell">
                        {new Date(delivery.createdAt).toLocaleString()}
                      </td>
                      <td>
                        {delivery.lastError ? (
                          <code className="delivery-error-code" title={delivery.lastError}>
                            {delivery.lastError}
                          </code>
                        ) : (
                          <span className="dash-empty">-</span>
                        )}
                      </td>
                      <td className="td-actions">
                        {delivery.state === "failed" && (
                          <button
                            className="icon-button retry-button"
                            type="button"
                            title={`Retry delivery ${delivery.id}`}
                            aria-label={`Retry delivery ${delivery.id}`}
                            disabled={busy}
                            onClick={() => void action(() => notificationApi.retryDelivery(projectKey, delivery.id), "Delivery queued.")}
                          >
                            <RefreshCw size={15} />
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </section>
  );
}

function ChannelEditor({
  channel,
  busy,
  onClose,
  onSave,
}: {
  channel?: NotificationChannel;
  busy: boolean;
  onClose: () => void;
  onSave: (input: NotificationChannelInput) => void;
}) {
  const [name, setName] = useState(channel?.name || "");
  const [platform, setPlatform] = useState<NotificationChannel["platform"]>(channel?.platform || "telegram");
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [replace, setReplace] = useState(!channel);
  const [token, setToken] = useState("");
  const [chatId, setChatId] = useState("");
  const [webhook, setWebhook] = useState("");
  const [signing, setSigning] = useState("");
  const [phoneNumberId, setPhoneNumberId] = useState("");

  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSave({
      name,
      platform,
      enabled,
      ...(replace
        ? {
            credentials:
              platform === "telegram"
                ? { botToken: token.trim(), chatId: chatId.trim() }
                : platform === "whatsapp"
                  ? { botToken: token.trim(), phoneNumberId: phoneNumberId.trim(), chatId: chatId.trim() }
                  : { webhookUrl: webhook.trim(), ...(platform === "feishu" ? { signingSecret: signing } : {}) },
          }
        : {}),
    });
  };

  return (
    <form className="notification-editor" aria-label={channel ? "Edit channel" : "Add channel"} onSubmit={submit}>
      <div className="notification-editor-heading">
        <div className="editor-title-group">
          <div className="editor-title-icon">
            <Radio size={18} />
          </div>
          <div>
            <h2>{channel ? "Edit channel" : "Add channel"}</h2>
            <p className="editor-title-sub">
              {channel
                ? `Update channel parameters and credentials for ${channel.name}`
                : "Set up a new notification destination for instant alerts and resolution reports"}
            </p>
          </div>
        </div>
        <button
          className="icon-button"
          title="Close channel editor"
          aria-label="Close channel editor"
          type="button"
          disabled={busy}
          onClick={onClose}
        >
          <X size={16} />
        </button>
      </div>

      <div className="notification-fields">
        {/* Name Field */}
        <div className="field-group">
          <label>
            Name
            <input
              required
              maxLength={100}
              placeholder={platformDetails[platform].namePlaceholder}
              value={name}
              disabled={busy}
              onChange={e => setName(e.target.value)}
            />
          </label>
          <span className="field-hint">A unique, recognizable display name for this notification target.</span>
        </div>

        {/* Platform Field */}
        <div className="field-group">
          <label>
            Platform
            <select
              value={platform}
              disabled={!!channel || busy}
              onChange={e => {
                setPlatform(e.target.value as NotificationChannel["platform"]);
                setToken("");
                setChatId("");
                setWebhook("");
                setSigning("");
                setPhoneNumberId("");
              }}
            >
              {Object.entries(platforms).map(([value, label]) => (
                <option value={value} key={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <span className="field-hint">{platformDetails[platform].hint}</span>
        </div>

        {/* Checkbox Options */}
        <div className="checkbox-row">
          <label className="notification-checkbox">
            <input type="checkbox" checked={enabled} disabled={busy} onChange={e => setEnabled(e.target.checked)} />
            Enabled
          </label>

          {channel && (
            <label className="notification-checkbox">
              <input type="checkbox" checked={replace} disabled={busy} onChange={e => setReplace(e.target.checked)} />
              Replace credentials
            </label>
          )}
        </div>

        {/* Credentials Box */}
        {replace && (
          <div className="credentials-panel">
            <div className="credentials-panel-header">
              <Key size={15} />
              <span>{platforms[platform]} Credentials</span>
            </div>
            <div className="credentials-panel-fields">
              {platform === "telegram" ? (
                <>
                  <div className="field-group">
                    <label>
                      Bot token
                      <input
                        type="password"
                        autoComplete="new-password"
                        required
                        maxLength={250}
                        disabled={busy}
                        value={token}
                        placeholder="123456789:ABCdefGhIJKlmNoPQRsTUVwxyZ"
                        onChange={e => setToken(e.target.value)}
                      />
                    </label>
                    <span className="field-hint">Obtained from @BotFather in Telegram.</span>
                  </div>
                  <div className="field-group">
                    <label>
                      Chat ID
                      <input
                        required
                        maxLength={100}
                        disabled={busy}
                        value={chatId}
                        placeholder="e.g. -100123456789 or @channelname"
                        onChange={e => setChatId(e.target.value)}
                      />
                    </label>
                    <span className="field-hint">Target group or channel chat ID.</span>
                  </div>
                </>
              ) : platform === "whatsapp" ? (
                <>
                  <div className="field-group full-width">
                    <label>
                      Access token
                      <input
                        type="password"
                        autoComplete="new-password"
                        required
                        maxLength={1000}
                        disabled={busy}
                        value={token}
                        placeholder="EAAG..."
                        onChange={e => setToken(e.target.value)}
                      />
                    </label>
                    <span className="field-hint">Permanent system-user token with the whatsapp_business_messaging permission.</span>
                  </div>
                  <div className="field-group">
                    <label>
                      Phone number ID
                      <input
                        required
                        maxLength={30}
                        disabled={busy}
                        value={phoneNumberId}
                        placeholder="e.g. 106540352242922"
                        onChange={e => setPhoneNumberId(e.target.value)}
                      />
                    </label>
                    <span className="field-hint">Sender phone number ID from WhatsApp Manager / API Setup.</span>
                  </div>
                  <div className="field-group">
                    <label>
                      Recipient number
                      <input
                        required
                        maxLength={16}
                        disabled={busy}
                        value={chatId}
                        placeholder="e.g. +15551234567"
                        onChange={e => setChatId(e.target.value)}
                      />
                    </label>
                    <span className="field-hint">E.164 number. Free-form text only arrives within 24h of the recipient's last message.</span>
                  </div>
                </>
              ) : (
                <>
                  <div className="field-group full-width">
                    <label>
                      Webhook URL
                      <input
                        type="password"
                        autoComplete="new-password"
                        required
                        maxLength={1000}
                        disabled={busy}
                        value={webhook}
                        placeholder={platformDetails[platform].webhookPlaceholder}
                        onChange={e => setWebhook(e.target.value)}
                      />
                    </label>
                    <span className="field-hint">Webhook URL from your group bot settings.</span>
                  </div>
                  {platform === "feishu" && (
                    <div className="field-group full-width">
                      <label>
                        Signing secret (optional)
                        <input
                          type="password"
                          autoComplete="new-password"
                          maxLength={500}
                          disabled={busy}
                          value={signing}
                          placeholder="Optional Feishu webhook signing secret"
                          onChange={e => setSigning(e.target.value)}
                        />
                      </label>
                      <span className="field-hint">Only required if signature verification is toggled on in Feishu.</span>
                    </div>
                  )}
                </>
              )}
            </div>
          </div>
        )}
      </div>

      <div className="editor-actions-row">
        <button className="primary-button" type="submit" disabled={busy}>
          <Save size={15} />
          {busy ? "Saving..." : "Save channel"}
        </button>
        <button className="secondary-button" type="button" disabled={busy} onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}
