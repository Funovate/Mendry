-- Explicit development-only project and incident fixtures. This file is never
-- embedded or run by API/migrate startup. Stable UUIDs and keys make repeated
-- execution safe. It intentionally creates no user, password, or credential.

INSERT INTO projects (id, project_key, name, description)
VALUES (
    '019ff544-405c-7d01-8f10-cb3fc579605c',
    'demo-incident',
    'Demo incident service',
    'Local project for the project-scoped incident MVP.'
)
ON CONFLICT DO NOTHING;

INSERT INTO project_environments (id, project_id, environment_key, name, service)
VALUES (
    '019ff544-405c-7d02-8f10-cb3fc579605c',
    '019ff544-405c-7d01-8f10-cb3fc579605c',
    'production',
    'Production',
    'incident-api'
)
ON CONFLICT DO NOTHING;

INSERT INTO project_repositories (
    id, project_id, remote_url, scm_provider, transport, production_branch, deployed_commit
)
VALUES (
    '019ff544-405c-7d07-8f10-cb3fc579605c',
    '019ff544-405c-7d01-8f10-cb3fc579605c',
    'https://github.com/example/incident-api.git',
    'github',
    'https',
    'main',
    '0123456789abcdef0123456789abcdef01234567'
)
ON CONFLICT DO NOTHING;

INSERT INTO project_sources (
    id, project_id, environment_id, kind, config, capabilities
)
VALUES (
    '019ff544-405c-7d08-8f10-cb3fc579605c',
    '019ff544-405c-7d01-8f10-cb3fc579605c',
    '019ff544-405c-7d02-8f10-cb3fc579605c',
    'cloud',
    '{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"demo-logset"}'::jsonb,
    ARRAY['push_ingestion', 'pull_collection']::text[]
)
ON CONFLICT DO NOTHING;

INSERT INTO project_triggers (
    id, project_id, environment_id, kind, config
)
VALUES (
    '019ff544-405c-7d09-8f10-cb3fc579605c',
    '019ff544-405c-7d01-8f10-cb3fc579605c',
    '019ff544-405c-7d02-8f10-cb3fc579605c',
    'custom_rule',
    '{"schemaVersion":1,"groupingWindowSeconds":300,"matchExpression":"level >= error"}'::jsonb
)
ON CONFLICT DO NOTHING;

INSERT INTO incidents (
    id, project_id, environment_id, source_id, incident_number, title,
    fingerprint, status, priority, source, first_seen, last_seen,
    occurrence_count, host_count, muted, notification_summary
)
VALUES
    ('019ff544-405c-7d03-bf10-cb3fc579605c',
     '019ff544-405c-7d01-8f10-cb3fc579605c',
     '019ff544-405c-7d02-8f10-cb3fc579605c',
     '019ff544-405c-7d08-8f10-cb3fc579605c', 2048,
     'Validator locale fr is not registered', 'b2a8:validator-locale',
     'Open', 'Info', 'cloud',
     '2026-08-08T15:35:12.109Z', '2026-08-08T15:52:14.750Z', 47, 2, false,
     'Lifecycle default'),
    ('019ff544-405c-7d04-8f10-cb3fc579605c',
     '019ff544-405c-7d01-8f10-cb3fc579605c',
     '019ff544-405c-7d02-8f10-cb3fc579605c',
     '019ff544-405c-7d08-8f10-cb3fc579605c', 2039,
     'PostgreSQL pool acquisition timeout', 'a710:pg-pool-timeout',
     'Recovered', 'P2', 'cloud',
     '2026-08-08T14:10:00Z', '2026-08-08T15:12:00Z', 186, 4, false,
     'Recovery sent'),
    ('019ff544-405c-7d05-9f10-cb3fc579605c',
     '019ff544-405c-7d01-8f10-cb3fc579605c',
     '019ff544-405c-7d02-8f10-cb3fc579605c',
     '019ff544-405c-7d08-8f10-cb3fc579605c', 2024,
     'Connector retry exhausted', '6c43:connector-retry',
     'Open', 'Info', 'cloud',
     '2026-08-08T13:50:00Z', '2026-08-08T14:53:00Z', 8, 1, true,
     'Muted'),
    ('019ff544-405c-7d06-af10-cb3fc579605c',
     '019ff544-405c-7d01-8f10-cb3fc579605c',
     '019ff544-405c-7d02-8f10-cb3fc579605c',
     '019ff544-405c-7d08-8f10-cb3fc579605c', 1988,
     'Configuration checksum mismatch', 'e081:configuration-checksum',
     'Closed', 'Info', 'cloud',
     '2026-08-08T15:40:00Z', '2026-08-08T16:03:00Z', 19, 1, false,
     'Closed by operator')
ON CONFLICT DO NOTHING;

SELECT setval(
    pg_get_serial_sequence('incidents', 'incident_number'),
    GREATEST(2048, (SELECT max(incident_number) FROM incidents))
);
