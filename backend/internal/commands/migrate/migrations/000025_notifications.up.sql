-- Additive migration: create notification tables and indexes; only FK setup locks existing parents.
-- Deploy before the API binary; business state and delivery snapshots share the caller's transaction.
CREATE TABLE notification_channels (
 id uuid PRIMARY KEY,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
 platform text NOT NULL CHECK (platform IN ('telegram', 'feishu', 'wecom')),
 enabled boolean NOT NULL DEFAULT true,
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 8192),
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (project_id, id)
);
CREATE INDEX notification_channels_project ON notification_channels(project_id);

CREATE TABLE notification_events (
 id uuid PRIMARY KEY,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 incident_id uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
 incident_number bigint NOT NULL,
 generation bigint NOT NULL,
 kind text NOT NULL CHECK (kind IN ('trigger', 'result')),
 deduplication_key text NOT NULL UNIQUE,
 message text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notification_deliveries (
 id uuid PRIMARY KEY,
 sequence bigserial UNIQUE NOT NULL,
 event_id uuid NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 channel_id uuid NOT NULL,
 channel_name text NOT NULL,
 platform text NOT NULL,
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 8192),
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sending', 'delivered', 'failed', 'cancelled')),
 attempts integer NOT NULL DEFAULT 0,
 last_error text NOT NULL DEFAULT '',
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_token uuid,
 lease_until timestamptz,
 CHECK (state <> 'cancelled' OR (lease_token IS NULL AND lease_until IS NULL)),
 delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (event_id, channel_id)
);
CREATE INDEX notification_deliveries_claim ON notification_deliveries(next_attempt_at, sequence) WHERE state IN ('pending', 'sending');
CREATE INDEX notification_deliveries_order ON notification_deliveries(project_id, channel_id, sequence);
COMMENT ON TABLE notification_events IS 'Transactionally captured trigger or first AI stopping result per incident lifecycle; no historical backfill.';
COMMENT ON TABLE notification_deliveries IS 'At-least-once delivery with immutable encrypted audit snapshot, current enabled channel credentials at claim, cancellation, and fenced worker lease.';
