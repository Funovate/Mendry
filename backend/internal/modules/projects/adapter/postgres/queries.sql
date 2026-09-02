-- name: CreateProject :one
WITH created_project AS (
    INSERT INTO projects (id, project_key, name, description)
    VALUES (sqlc.arg(project_id), sqlc.arg(project_key), sqlc.arg(name), sqlc.arg(description))
    RETURNING id, project_key, name, description, version, created_at, updated_at
), created_membership AS (
    INSERT INTO project_memberships (project_id, user_id, role)
    SELECT id, sqlc.arg(actor_user_id), 'admin'
    FROM created_project
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), id, sqlc.arg(actor_user_id), 'project.created', 'project', id,
           'Project created.', jsonb_build_object('projectKey', project_key)
    FROM created_project
)
SELECT id, project_key, name, description, 'admin'::text AS role, version, created_at, updated_at
FROM created_project;

-- name: ListProjectsForUser :many
SELECT p.id, p.project_key, p.name, p.description,
       COALESCE(CASE WHEN sqlc.arg(system_admin)::boolean THEN 'admin'::text ELSE pm.role END, 'admin')::text AS role,
       p.version, p.created_at, p.updated_at,
       COUNT(*) OVER() AS total_count
FROM projects AS p
LEFT JOIN project_memberships AS pm
    ON pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)
WHERE sqlc.arg(system_admin)::boolean OR pm.user_id IS NOT NULL
ORDER BY p.name, p.project_key
LIMIT sqlc.arg(result_limit);

-- name: GetProjectAccess :one
SELECT p.id, p.project_key, p.name, p.description,
       COALESCE(CASE WHEN sqlc.arg(system_admin)::boolean THEN 'admin'::text ELSE pm.role END, 'admin')::text AS role,
       p.version, p.created_at, p.updated_at
FROM projects AS p
LEFT JOIN project_memberships AS pm
    ON pm.project_id = p.id AND pm.user_id = sqlc.arg(user_id)
WHERE p.project_key = sqlc.arg(project_key)
  AND (sqlc.arg(system_admin)::boolean OR pm.user_id IS NOT NULL);

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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), changed_project.id, sqlc.arg(actor_user_id),
           'project.renamed', 'project', changed_project.id,
           'Project renamed.', jsonb_build_object('projectKey', changed_project.project_key)
    FROM changed_project
)
SELECT changed_project.id, changed_project.project_key, changed_project.name, changed_project.description,
       sqlc.arg(role)::text AS role, changed_project.version, changed_project.created_at, changed_project.updated_at
FROM changed_project;

-- name: ListProjectMembers :many
SELECT u.id AS user_id, u.username, pm.role, pm.version, pm.created_at, pm.updated_at,
       COUNT(*) OVER() AS total_count
FROM project_memberships AS pm
JOIN users AS u ON u.id = pm.user_id
WHERE pm.project_id = sqlc.arg(project_id)
ORDER BY u.username;

-- name: GetEnabledProjectUser :one
SELECT id, username
FROM users
WHERE username = sqlc.arg(username) AND enabled;

-- name: GetProjectMemberByUsername :one
SELECT u.id AS user_id, u.username, pm.role, pm.version, pm.created_at, pm.updated_at
FROM project_memberships AS pm
JOIN users AS u ON u.id = pm.user_id
WHERE pm.project_id = sqlc.arg(project_id)
  AND u.username = sqlc.arg(username);

-- name: UpsertProjectMember :one
WITH target_user AS (
    SELECT id, username
    FROM users
    WHERE username = sqlc.arg(username) AND enabled
), changed_membership AS (
    INSERT INTO project_memberships (project_id, user_id, role)
    SELECT sqlc.arg(project_id), id, sqlc.arg(role)
    FROM target_user
    ON CONFLICT (project_id, user_id) DO UPDATE
    SET role = EXCLUDED.role,
        version = project_memberships.version + 1,
        updated_at = clock_timestamp()
    WHERE project_memberships.role <> 'admin'
       OR EXCLUDED.role = 'admin'
       OR EXISTS (
           SELECT 1
           FROM project_memberships AS another_admin
           WHERE another_admin.project_id = project_memberships.project_id
             AND another_admin.role = 'admin'
             AND another_admin.user_id <> project_memberships.user_id
       )
    RETURNING user_id, role, version, created_at, updated_at
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.member.updated', 'project_membership', changed_membership.user_id,
           'Project member updated.',
           jsonb_build_object('username', target_user.username, 'role', changed_membership.role)
    FROM changed_membership
    JOIN target_user ON target_user.id = changed_membership.user_id
)
SELECT changed_membership.user_id, target_user.username, changed_membership.role,
       changed_membership.version, changed_membership.created_at, changed_membership.updated_at
FROM changed_membership
JOIN target_user ON target_user.id = changed_membership.user_id;

-- name: DeleteProjectMember :one
WITH target_membership AS (
    SELECT pm.user_id, u.username, pm.role
    FROM project_memberships AS pm
    JOIN users AS u ON u.id = pm.user_id
    WHERE pm.project_id = sqlc.arg(project_id)
      AND u.username = sqlc.arg(username)
      AND (
          pm.role <> 'admin'
          OR EXISTS (
              SELECT 1
              FROM project_memberships AS another_admin
              WHERE another_admin.project_id = pm.project_id
                AND another_admin.role = 'admin'
                AND another_admin.user_id <> pm.user_id
          )
      )
), deleted_membership AS (
    DELETE FROM project_memberships
    WHERE project_id = sqlc.arg(project_id)
      AND user_id = (SELECT user_id FROM target_membership)
    RETURNING user_id
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.member.removed', 'project_membership', deleted_membership.user_id,
           'Project member removed.', jsonb_build_object('username', target_membership.username)
    FROM deleted_membership
    JOIN target_membership ON target_membership.user_id = deleted_membership.user_id
)
SELECT target_membership.user_id, target_membership.username, target_membership.role
FROM target_membership
JOIN deleted_membership ON deleted_membership.user_id = target_membership.user_id;

-- name: CreateProjectSecret :one
WITH created_secret AS (
    INSERT INTO project_secrets (id, project_id, name, kind, ciphertext, nonce, key_version)
    VALUES (sqlc.arg(secret_id), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(kind),
            sqlc.arg(ciphertext), sqlc.arg(nonce), sqlc.arg(key_version))
    RETURNING id, project_id, name, kind, key_version, version, created_at, updated_at
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), project_id, sqlc.arg(actor_user_id), 'project.secret.created',
           'project_secret', id, 'Project credential created.',
           jsonb_build_object('name', name, 'kind', kind)
    FROM created_secret
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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), changed_secret.project_id, sqlc.arg(actor_user_id), 'project.secret.updated',
           'project_secret', changed_secret.id, 'Project credential updated.',
           jsonb_build_object('name', changed_secret.name, 'kind', changed_secret.kind, 'rotated', sqlc.arg(rotated)::boolean)
    FROM changed_secret
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
SELECT agent_loop_mode, agent_loop_policy_version
FROM projects
WHERE id = sqlc.arg(project_id);

-- name: UpsertProjectRemediationPolicy :one
WITH changed_policy AS (
    UPDATE projects
    SET agent_loop_mode = sqlc.arg(agent_loop_mode),
        agent_loop_policy_version = projects.agent_loop_policy_version + 1,
        version = projects.version + 1,
        updated_at = clock_timestamp()
    WHERE projects.id = sqlc.arg(project_id)
    RETURNING id, agent_loop_mode, agent_loop_policy_version
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), changed_policy.id, sqlc.arg(actor_user_id),
           'project.remediation_policy.updated', 'project', changed_policy.id,
           'Project remediation policy updated.',
           jsonb_build_object('agentLoopMode', changed_policy.agent_loop_mode,
                              'policyVersion', changed_policy.agent_loop_policy_version)
    FROM changed_policy
)
SELECT agent_loop_mode, agent_loop_policy_version
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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.configuration.updated', 'project', sqlc.arg(project_id),
           'Project configuration updated.',
           jsonb_build_object(
               'environmentKey', changed_environment.environment_key,
               'sourceKind', changed_source.kind,
               'triggerKind', changed_trigger.kind,
               'llmProvider', changed_llm.provider,
               'llmModel', changed_llm.model
           )
    FROM changed_environment, changed_source, changed_trigger, changed_llm
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
SELECT trigger.project_id, source.id AS source_id
FROM project_triggers AS trigger
JOIN project_sources AS source
    ON source.project_id = trigger.project_id
   AND source.environment_id = trigger.environment_id
WHERE trigger.ingress_token_hash = sqlc.arg(ingress_token_hash)
  AND trigger.kind = 'signed_webhook'
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
      AND project_triggers.kind = 'signed_webhook'
    RETURNING id, project_id, version
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), changed_trigger.project_id, sqlc.arg(actor_user_id),
           'project.trigger.webhook_token', 'project_trigger', changed_trigger.id,
           'Project webhook token updated.',
           jsonb_build_object('rotated', sqlc.arg(rotated)::boolean)
    FROM changed_trigger
)
SELECT id, version
FROM changed_trigger;

-- name: ListProjectAuditEvents :many
SELECT id, project_id, actor_user_id, action, target_type, target_id, summary, metadata, occurred_at,
       COUNT(*) OVER() AS total_count
FROM audit_events
WHERE project_id = sqlc.arg(project_id)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(result_limit);

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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.configuration.updated', 'project', sqlc.arg(project_id),
           'Project environment configuration updated.',
           jsonb_build_object('environmentKey', changed_environment.environment_key)
    FROM changed_environment
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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.configuration.updated', 'project', sqlc.arg(project_id),
           'Project repository configuration updated.',
           jsonb_build_object('scmProvider', changed_repository.scm_provider, 'remoteUrl', changed_repository.remote_url)
    FROM changed_repository
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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.configuration.updated', 'project', sqlc.arg(project_id),
           'Project collection source configuration updated.',
           jsonb_build_object('sourceKind', changed_source.kind)
    FROM changed_source
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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.configuration.updated', 'project', sqlc.arg(project_id),
           'Project trigger configuration updated.',
           jsonb_build_object('triggerKind', changed_trigger.kind)
    FROM changed_trigger
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
), created_audit AS (
    INSERT INTO audit_events (id, project_id, actor_user_id, action, target_type, target_id, summary, metadata)
    SELECT sqlc.arg(audit_id), sqlc.arg(project_id), sqlc.arg(actor_user_id),
           'project.configuration.updated', 'project', sqlc.arg(project_id),
           'Project LLM provider configuration updated.',
           jsonb_build_object('llmProvider', changed_llm.provider, 'llmModel', changed_llm.model)
    FROM changed_llm
 )
SELECT id, provider, base_url, credential_secret_id, model, version
FROM changed_llm;
