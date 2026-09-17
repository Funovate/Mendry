-- Overview reads existing run totals; only new increments have a known time.
ALTER TABLE remediation_run ADD COLUMN state_entered_at timestamptz;
COMMENT ON COLUMN remediation_run.state_entered_at IS
    'Time the current state was entered; NULL for legacy runs until their next state transition.';

CREATE TABLE overview_collection (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO overview_collection (singleton) VALUES (true);
COMMENT ON TABLE overview_collection IS 'Start of accurate remediation Token time series; historical totals are not backfilled.';
COMMENT ON COLUMN overview_collection.singleton IS 'Exactly one collection epoch per database.';
COMMENT ON COLUMN overview_collection.enabled_at IS 'Collection activation time, independent of the first usage event.';

CREATE TABLE remediation_token_usage (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    tokens_in bigint NOT NULL CHECK (tokens_in >= 0),
    tokens_out bigint NOT NULL CHECK (tokens_out >= 0),
    CHECK (tokens_in > 0 OR tokens_out > 0)
);
CREATE INDEX remediation_token_usage_run_time_idx ON remediation_token_usage (run_id, recorded_at);
CREATE INDEX remediation_token_usage_time_idx ON remediation_token_usage (recorded_at);
COMMENT ON TABLE remediation_token_usage IS 'Transactional positive deltas of provider-reported run Token counters; excludes raw model content.';
COMMENT ON COLUMN remediation_token_usage.id IS 'Unique committed usage event identity.';
COMMENT ON COLUMN remediation_token_usage.run_id IS 'Attempt whose cumulative counters increased.';
COMMENT ON COLUMN remediation_token_usage.recorded_at IS 'Database accounting time of the increment, not the upstream request start time.';
COMMENT ON COLUMN remediation_token_usage.tokens_in IS 'Input Token increment.';
COMMENT ON COLUMN remediation_token_usage.tokens_out IS 'Output Token increment.';

CREATE FUNCTION overview_run_state_time() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.state_entered_at := NEW.started_at;
    ELSIF NEW.state IS DISTINCT FROM OLD.state THEN
        NEW.state_entered_at := clock_timestamp();
    ELSE
        NEW.state_entered_at := OLD.state_entered_at;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER overview_run_state_time_trigger
    BEFORE INSERT OR UPDATE OF state, state_entered_at ON remediation_run
    FOR EACH ROW EXECUTE FUNCTION overview_run_state_time();

CREATE FUNCTION overview_record_token_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    delta_in bigint;
    delta_out bigint;
BEGIN
    IF TG_OP = 'INSERT' THEN
        delta_in := NEW.model_tokens_in;
        delta_out := NEW.model_tokens_out;
    ELSE
        delta_in := NEW.model_tokens_in - OLD.model_tokens_in;
        delta_out := NEW.model_tokens_out - OLD.model_tokens_out;
    END IF;
    IF delta_in < 0 OR delta_out < 0 THEN
        RAISE EXCEPTION 'Remediation Token counters must not decrease' USING ERRCODE = '23514';
    END IF;
    IF delta_in > 0 OR delta_out > 0 THEN
        INSERT INTO remediation_token_usage (run_id, tokens_in, tokens_out)
        VALUES (NEW.id, delta_in, delta_out);
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER overview_record_token_usage_trigger
    AFTER INSERT OR UPDATE OF model_tokens_in, model_tokens_out ON remediation_run
    FOR EACH ROW EXECUTE FUNCTION overview_record_token_usage();
