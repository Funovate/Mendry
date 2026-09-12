-- Mendry supports exactly one login identity. Refuse ambiguous upgrades rather
-- than silently selecting or deleting an existing account.
DO $$
BEGIN
    IF (SELECT count(*) FROM users) > 1 THEN
        RAISE EXCEPTION 'Single-user migration requires at most one user; explicitly select the account to retain before retrying';
    END IF;
END $$;

CREATE UNIQUE INDEX users_single_user ON users ((true));

DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS project_memberships;
ALTER TABLE users DROP COLUMN role;

COMMENT ON TABLE users IS 'Single login identity; at most one account, created by bootstrap-admin.';
