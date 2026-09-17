CREATE TABLE project_validation_preparations (
    project_id uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    target_commit text NOT NULL CHECK (target_commit ~ '^[0-9A-Fa-f]{40}$' OR target_commit ~ '^[0-9A-Fa-f]{64}$'),
    service_directory text NOT NULL CHECK (length(service_directory) BETWEEN 1 AND 512),
    status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'blocked')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX project_validation_preparations_status_idx
    ON project_validation_preparations(status, updated_at);
