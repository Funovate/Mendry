-- name: CreateProject :one
INSERT INTO projects (id, project_key, name, description)
VALUES (sqlc.arg(project_id), sqlc.arg(project_key), sqlc.arg(name), sqlc.arg(description))
RETURNING id, project_key, name, description, version, created_at, updated_at;

-- name: ListProjects :many
SELECT id, project_key, name, description, version, created_at, updated_at,
       COUNT(*) OVER() AS total_count
FROM projects
ORDER BY name, project_key
LIMIT sqlc.arg(result_limit);

-- name: GetProject :one
SELECT id, project_key, name, description, version, created_at, updated_at
FROM projects
WHERE project_key = sqlc.arg(project_key);

-- name: UpdateProjectName :one
WITH existing_project AS (
    SELECT projects.id, projects.project_key, projects.name, projects.description, projects.version, projects.created_at
    FROM projects
    WHERE projects.id = sqlc.arg(project_id)
), changed_project AS (
    UPDATE projects
    SET name = sqlc.arg(name),
        version = projects.version + 1,
        updated_at = clock_timestamp()
    WHERE projects.id = sqlc.arg(project_id)
    RETURNING id, project_key, name, description, version, created_at, updated_at
), synchronized_environment AS (
    UPDATE project_environments
    SET name = sqlc.arg(name),
        version = project_environments.version + 1,
        updated_at = clock_timestamp()
    FROM existing_project
    WHERE project_environments.project_id = existing_project.id
      AND project_environments.name = existing_project.name
)
SELECT id, project_key, name, description, version, created_at, updated_at
FROM changed_project;

-- name: CreateProjectSecret :one
WITH created_secret AS (
    INSERT INTO project_secrets (id, project_id, name, kind, ciphertext, nonce, key_version)
    VALUES (sqlc.arg(secret_id), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(kind),
            sqlc.arg(ciphertext), sqlc.arg(nonce), sqlc.arg(key_version))
    RETURNING id, project_id, name, kind, key_version, version, created_at, updated_at
)
SELECT id, project_id, name, kind, key_version, version, created_at, updated_at
FROM created_secret;

-- name: ListProjectSecrets :many
SELECT id, project_id, name, kind, key_version, version, created_at, updated_at,
       COUNT(*) OVER() AS total_count
FROM project_secrets
WHERE project_id = sqlc.arg(project_id)
ORDER BY name;

-- name: GetProjectSecret :one
SELECT id, project_id, name, kind, ciphertext, nonce, key_version, version, created_at, updated_at
FROM project_secrets
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(secret_id);

-- name: UpdateProjectSecret :one
WITH changed_secret AS (
    UPDATE project_secrets
    SET name = sqlc.arg(name),
        ciphertext = sqlc.arg(ciphertext),
        nonce = sqlc.arg(nonce),
        key_version = sqlc.arg(key_version),
        version = project_secrets.version + 1,
        updated_at = clock_timestamp()
    WHERE project_secrets.project_id = sqlc.arg(project_id) AND project_secrets.id = sqlc.arg(secret_id)
    RETURNING id, project_id, name, kind, key_version, version, created_at, updated_at
)
SELECT changed_secret.id, changed_secret.project_id, changed_secret.name, changed_secret.kind,
       changed_secret.key_version, changed_secret.version, changed_secret.created_at, changed_secret.updated_at
FROM changed_secret;

-- name: GetProjectConfiguration :one
SELECT e.id AS environment_id, e.environment_key, e.name AS environment_name, e.service,
       e.version AS environment_version,
       r.id AS repository_id, r.remote_url, r.scm_provider, r.transport,
       r.credential_secret_id AS repository_credential_secret_id,
       r.production_branch, r.deployed_commit, r.version AS repository_version,
       s.id AS source_id, s.kind AS source_kind,
       s.credential_secret_id AS source_credential_secret_id, s.config AS source_config,
       s.capabilities AS source_capabilities, s.enabled AS source_enabled, s.version AS source_version,
       t.id AS trigger_id, t.kind AS trigger_kind,
       t.signing_secret_id, t.config AS trigger_config, t.enabled AS trigger_enabled,
       t.version AS trigger_version,
       t.ingress_token_hash, t.ingress_token_ciphertext, t.ingress_token_nonce,
       l.id AS llm_id, l.provider AS llm_provider, l.base_url AS llm_base_url,
       l.credential_secret_id AS llm_credential_secret_id, l.model AS llm_model,
       l.version AS llm_version
FROM project_environments AS e
JOIN project_repositories AS r ON r.project_id = e.project_id
JOIN project_sources AS s ON s.project_id = e.project_id AND s.environment_id = e.id
JOIN project_triggers AS t ON t.project_id = e.project_id AND t.environment_id = e.id
LEFT JOIN project_llm_providers AS l ON l.project_id = e.project_id
WHERE e.project_id = sqlc.arg(project_id)
ORDER BY e.created_at
LIMIT 1;

-- name: GetProjectRemediationPolicy :one
SELECT agent_loop_mode, remediation_execution_mode, remediation_validation_profile,
       remediation_publication, remediation_change_policy, agent_loop_policy_version
FROM projects
WHERE id = sqlc.arg(project_id);

-- name: UpsertProjectRemediationPolicy :one
WITH changed_policy AS (
    UPDATE projects
    SET agent_loop_mode = sqlc.arg(agent_loop_mode),
        remediation_execution_mode = sqlc.arg(remediation_execution_mode),
        remediation_validation_profile = sqlc.arg(remediation_validation_profile),
        remediation_publication = sqlc.arg(remediation_publication),
        remediation_change_policy = sqlc.arg(remediation_change_policy),
        agent_loop_policy_version = projects.agent_loop_policy_version + 1,
        version = projects.version + 1,
        updated_at = clock_timestamp()
    WHERE projects.id = sqlc.arg(project_id)
    RETURNING id, agent_loop_mode, remediation_execution_mode, remediation_validation_profile,
              remediation_publication, remediation_change_policy, agent_loop_policy_version
)
SELECT agent_loop_mode, remediation_execution_mode, remediation_validation_profile,
       remediation_publication, remediation_change_policy, agent_loop_policy_version
FROM changed_policy;

-- name: UpsertProjectConfiguration :one
WITH changed_environment AS (
    INSERT INTO project_environments (id, project_id, environment_key, name, service)
    VALUES (sqlc.arg(environment_id), sqlc.arg(project_id), sqlc.arg(environment_key),
            sqlc.arg(environment_name), sqlc.narg(service))
    ON CONFLICT (project_id) DO UPDATE
    SET environment_key = EXCLUDED.environment_key,
        name = EXCLUDED.name,
        service = EXCLUDED.service,
        version = project_environments.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, environment_key, name, service, version
), changed_repository AS (
    INSERT INTO project_repositories (
        id, project_id, remote_url, scm_provider, transport, credential_secret_id,
        production_branch, deployed_commit
    ) VALUES (
        sqlc.arg(repository_id), sqlc.arg(project_id), sqlc.arg(remote_url),
        sqlc.arg(scm_provider), sqlc.arg(repository_transport),
        sqlc.narg(repository_credential_secret_id), sqlc.arg(production_branch),
        sqlc.arg(deployed_commit)
    )
    ON CONFLICT (project_id) DO UPDATE
    SET remote_url = EXCLUDED.remote_url,
        scm_provider = EXCLUDED.scm_provider,
        transport = EXCLUDED.transport,
        credential_secret_id = EXCLUDED.credential_secret_id,
        production_branch = EXCLUDED.production_branch,
        deployed_commit = EXCLUDED.deployed_commit,
        version = project_repositories.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, remote_url, scm_provider, transport, credential_secret_id,
              production_branch, deployed_commit, version
), changed_source AS (
    INSERT INTO project_sources (
        id, project_id, environment_id, kind, credential_secret_id,
        config, capabilities, enabled
    )
    SELECT sqlc.arg(source_id), sqlc.arg(project_id), changed_environment.id,
           sqlc.arg(source_kind), sqlc.narg(source_credential_secret_id),
           sqlc.arg(source_config), sqlc.arg(source_capabilities), sqlc.arg(source_enabled)
    FROM changed_environment
    ON CONFLICT (project_id) DO UPDATE
    SET environment_id = EXCLUDED.environment_id,
        kind = EXCLUDED.kind,
        credential_secret_id = EXCLUDED.credential_secret_id,
        config = EXCLUDED.config,
        capabilities = EXCLUDED.capabilities,
        enabled = EXCLUDED.enabled,
        version = project_sources.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, kind, credential_secret_id, config, capabilities, enabled, version
), changed_trigger AS (
    INSERT INTO project_triggers (
        id, project_id, environment_id, kind, signing_secret_id, config, enabled,
        ingress_token_hash, ingress_token_ciphertext, ingress_token_nonce
    )
    SELECT sqlc.arg(trigger_id), sqlc.arg(project_id), changed_environment.id,
           sqlc.arg(trigger_kind), sqlc.narg(signing_secret_id),
           sqlc.arg(trigger_config), sqlc.arg(trigger_enabled),
           sqlc.narg(ingress_token_hash), sqlc.narg(ingress_token_ciphertext), sqlc.narg(ingress_token_nonce)
    FROM changed_environment
    ON CONFLICT (project_id) DO UPDATE
    SET environment_id = EXCLUDED.environment_id,
        kind = EXCLUDED.kind,
        signing_secret_id = EXCLUDED.signing_secret_id,
        config = EXCLUDED.config,
        enabled = EXCLUDED.enabled,
        ingress_token_hash = EXCLUDED.ingress_token_hash,
        ingress_token_ciphertext = EXCLUDED.ingress_token_ciphertext,
        ingress_token_nonce = EXCLUDED.ingress_token_nonce,
        version = project_triggers.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, kind, signing_secret_id, config, enabled, version,
              ingress_token_hash, ingress_token_ciphertext, ingress_token_nonce
), changed_llm AS (
    INSERT INTO project_llm_providers (
        id, project_id, provider, base_url, credential_secret_id, model
    ) VALUES (
        sqlc.arg(llm_id), sqlc.arg(project_id), sqlc.arg(llm_provider),
        sqlc.arg(llm_base_url), sqlc.arg(llm_credential_secret_id), sqlc.arg(llm_model)
    )
    ON CONFLICT (project_id) DO UPDATE
    SET provider = EXCLUDED.provider,
        base_url = EXCLUDED.base_url,
        credential_secret_id = EXCLUDED.credential_secret_id,
        model = EXCLUDED.model,
        version = project_llm_providers.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, provider, base_url, credential_secret_id, model, version
)
SELECT changed_environment.id AS environment_id,
       changed_environment.environment_key, changed_environment.name AS environment_name,
       changed_environment.service, changed_environment.version AS environment_version,
       changed_repository.id AS repository_id, changed_repository.remote_url,
       changed_repository.scm_provider, changed_repository.transport,
       changed_repository.credential_secret_id AS repository_credential_secret_id,
       changed_repository.production_branch, changed_repository.deployed_commit,
       changed_repository.version AS repository_version,
       changed_source.id AS source_id, changed_source.kind AS source_kind,
       changed_source.credential_secret_id AS source_credential_secret_id,
       changed_source.config AS source_config, changed_source.capabilities AS source_capabilities,
       changed_source.enabled AS source_enabled, changed_source.version AS source_version,
       changed_trigger.id AS trigger_id, changed_trigger.kind AS trigger_kind, changed_trigger.signing_secret_id,
       changed_trigger.config AS trigger_config, changed_trigger.enabled AS trigger_enabled,
       changed_trigger.version AS trigger_version,
       changed_trigger.ingress_token_hash, changed_trigger.ingress_token_ciphertext,
       changed_trigger.ingress_token_nonce,
       changed_llm.id AS llm_id, changed_llm.provider AS llm_provider,
       changed_llm.base_url AS llm_base_url,
       changed_llm.credential_secret_id AS llm_credential_secret_id,
       changed_llm.model AS llm_model, changed_llm.version AS llm_version
FROM changed_environment, changed_repository, changed_source, changed_trigger, changed_llm;

-- name: LookupWebhookToken :one
SELECT trigger.project_id, source.id AS source_id, trigger.id AS trigger_id,
       trigger.kind AS trigger_kind, trigger.version AS trigger_version,
       trigger.config AS trigger_config
FROM project_triggers AS trigger
JOIN project_sources AS source
    ON source.project_id = trigger.project_id
   AND source.environment_id = trigger.environment_id
WHERE trigger.ingress_token_hash = sqlc.arg(ingress_token_hash)
  AND trigger.kind IN ('signed_webhook', 'custom_rule')
  AND trigger.enabled
  AND source.enabled;

-- name: UpdateWebhookToken :one
WITH changed_trigger AS (
    UPDATE project_triggers
    SET ingress_token_hash = sqlc.arg(ingress_token_hash),
        ingress_token_ciphertext = sqlc.arg(ingress_token_ciphertext),
        ingress_token_nonce = sqlc.arg(ingress_token_nonce),
        version = project_triggers.version + 1,
        updated_at = clock_timestamp()
    WHERE project_triggers.project_id = sqlc.arg(project_id)
      AND project_triggers.kind IN ('signed_webhook', 'custom_rule')
    RETURNING id, project_id, version
)
SELECT id, version
FROM changed_trigger;

-- name: GetProjectEnvironment :one
SELECT id, environment_key, name, service, version
FROM project_environments
WHERE project_id = sqlc.arg(project_id)
ORDER BY created_at
LIMIT 1;

-- name: GetProjectRepository :one
SELECT id, remote_url, scm_provider, transport, credential_secret_id,
       production_branch, deployed_commit, version
FROM project_repositories
WHERE project_id = sqlc.arg(project_id)
LIMIT 1;

-- name: GetProjectSource :one
SELECT id, kind, credential_secret_id, config, capabilities, enabled, version
FROM project_sources
WHERE project_id = sqlc.arg(project_id)
LIMIT 1;

-- name: GetProjectTrigger :one
SELECT id, kind, signing_secret_id, config, enabled, version,
       ingress_token_hash, ingress_token_ciphertext, ingress_token_nonce
FROM project_triggers
WHERE project_id = sqlc.arg(project_id)
LIMIT 1;

-- name: GetProjectLLMProvider :one
SELECT id, provider, base_url, credential_secret_id, model, version
FROM project_llm_providers
WHERE project_id = sqlc.arg(project_id)
LIMIT 1;

-- name: UpsertProjectEnvironment :one
WITH changed_environment AS (
    INSERT INTO project_environments (id, project_id, environment_key, name, service)
    VALUES (sqlc.arg(environment_id), sqlc.arg(project_id), sqlc.arg(environment_key),
            sqlc.arg(environment_name), sqlc.narg(service))
    ON CONFLICT (project_id) DO UPDATE
    SET environment_key = EXCLUDED.environment_key,
        name = EXCLUDED.name,
        service = EXCLUDED.service,
        version = project_environments.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, environment_key, name, service, version
)
SELECT id, environment_key, name, service, version
FROM changed_environment;

-- name: UpsertProjectRepository :one
WITH changed_repository AS (
    INSERT INTO project_repositories (
        id, project_id, remote_url, scm_provider, transport, credential_secret_id,
        production_branch, deployed_commit
    ) VALUES (
        sqlc.arg(repository_id), sqlc.arg(project_id), sqlc.arg(remote_url),
        sqlc.arg(scm_provider), sqlc.arg(repository_transport),
        sqlc.narg(credential_secret_id), sqlc.arg(production_branch), sqlc.arg(deployed_commit)
    )
    ON CONFLICT (project_id) DO UPDATE
    SET remote_url = EXCLUDED.remote_url,
        scm_provider = EXCLUDED.scm_provider,
        transport = EXCLUDED.transport,
        credential_secret_id = EXCLUDED.credential_secret_id,
        production_branch = EXCLUDED.production_branch,
        deployed_commit = EXCLUDED.deployed_commit,
        version = project_repositories.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, remote_url, scm_provider, transport, credential_secret_id,
              production_branch, deployed_commit, version
)
SELECT id, remote_url, scm_provider, transport, credential_secret_id,
       production_branch, deployed_commit, version
FROM changed_repository;

-- name: UpsertProjectSource :one
WITH changed_source AS (
    INSERT INTO project_sources (
        id, project_id, environment_id, kind, credential_secret_id, config, capabilities, enabled
    ) VALUES (
        sqlc.arg(source_id), sqlc.arg(project_id), sqlc.arg(environment_id), sqlc.arg(source_kind),
        sqlc.narg(credential_secret_id), sqlc.arg(source_config), sqlc.arg(source_capabilities), sqlc.arg(source_enabled)
    )
    ON CONFLICT (project_id) DO UPDATE
    SET environment_id = EXCLUDED.environment_id,
        kind = EXCLUDED.kind,
        credential_secret_id = EXCLUDED.credential_secret_id,
        config = EXCLUDED.config,
        capabilities = EXCLUDED.capabilities,
        enabled = EXCLUDED.enabled,
        version = project_sources.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, kind, credential_secret_id, config, capabilities, enabled, version
)
SELECT id, kind, credential_secret_id, config, capabilities, enabled, version
FROM changed_source;

-- name: UpsertProjectTrigger :one
WITH changed_trigger AS (
    INSERT INTO project_triggers (
        id, project_id, environment_id, kind, signing_secret_id, config, enabled,
        ingress_token_hash, ingress_token_ciphertext, ingress_token_nonce
    ) VALUES (
        sqlc.arg(trigger_id), sqlc.arg(project_id), sqlc.arg(environment_id), sqlc.arg(trigger_kind),
        sqlc.narg(signing_secret_id), sqlc.arg(trigger_config), sqlc.arg(trigger_enabled),
        sqlc.narg(ingress_token_hash), sqlc.narg(ingress_token_ciphertext), sqlc.narg(ingress_token_nonce)
    )
    ON CONFLICT (project_id) DO UPDATE
    SET environment_id = EXCLUDED.environment_id,
        kind = EXCLUDED.kind,
        signing_secret_id = EXCLUDED.signing_secret_id,
        config = EXCLUDED.config,
        enabled = EXCLUDED.enabled,
        ingress_token_hash = EXCLUDED.ingress_token_hash,
        ingress_token_ciphertext = EXCLUDED.ingress_token_ciphertext,
        ingress_token_nonce = EXCLUDED.ingress_token_nonce,
        version = project_triggers.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, kind, signing_secret_id, config, enabled, version,
              ingress_token_hash, ingress_token_ciphertext, ingress_token_nonce
)
SELECT id, kind, signing_secret_id, config, enabled, version,
       ingress_token_hash, ingress_token_ciphertext, ingress_token_nonce
FROM changed_trigger;

-- name: UpsertProjectLLMProvider :one
WITH changed_llm AS (
    INSERT INTO project_llm_providers (
        id, project_id, provider, base_url, credential_secret_id, model
    ) VALUES (
        sqlc.arg(llm_id), sqlc.arg(project_id), sqlc.arg(llm_provider),
        sqlc.arg(llm_base_url), sqlc.arg(llm_credential_secret_id), sqlc.arg(llm_model)
    )
    ON CONFLICT (project_id) DO UPDATE
    SET provider = EXCLUDED.provider,
        base_url = EXCLUDED.base_url,
        credential_secret_id = EXCLUDED.credential_secret_id,
        model = EXCLUDED.model,
        version = project_llm_providers.version + 1,
        updated_at = clock_timestamp()
    RETURNING id, provider, base_url, credential_secret_id, model, version
)
SELECT id, provider, base_url, credential_secret_id, model, version
FROM changed_llm;
