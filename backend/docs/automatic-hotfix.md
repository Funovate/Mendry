# Automatic repair

Mendry's default automatic-remediation flow creates a constrained source patch and publishes it for review:

```text
diagnosis -> constrained patch -> Draft PR/MR -> repository CI -> human review and merge -> user CD
```

Repository CI is the engineering-validation authority in this mode. Mendry does not run project code, install dependencies, build a project image, merge the change, or deploy it. The project needs a Git write credential; supported SCM providers also need API access to create a Draft PR/MR. Generic Git and Yunxiao currently publish a review branch when the provider adapter cannot create a change request.

Automatic patches remain bounded by the immutable deployed commit and project change policy. Mendry rejects paths outside the allowlist, protected control-plane and credential paths, excessive files or lines, binary/symlink/submodule changes, and publication when the target branch has diverged. Publication always requires human review. The Draft PR/MR records the incident, deployed baseline, selected plan, and changed scope so repository CI and reviewers can make the final decision.

In **Project configuration -> Automatic hotfix**, choose **Draft PR**, select the Git write credential, and save. No Docker daemon, builder image, dependency preparation, or project validation command is required.

## Optional local pre-validation

**Mendry local pre-validation** is an optional enhanced mode. It runs an approved baseline and post-patch command in a network-disabled container before publication. Project users cannot provide arbitrary Dockerfiles, shell commands, images, install commands, or CI scripts. Mendry generates the profile from an independent Go module or locked npm project and selects an administrator-approved immutable Go or Node toolchain image.

Enhanced mode initially supports:

- Go modules with `go.sum` and tests.
- npm projects with `package-lock.json` and a real test script.
- One independent service directory per Mendry project.

npm workspaces, `go.work`, cross-directory local dependencies, unsupported runtimes, and missing approved toolchains fail closed. Dependency manifests, lockfiles, and runtime version files are added to the denied-path policy, so generated repairs remain source-only.

Enhanced validation runs without network access, Git credentials, or Docker socket access. Source is mounted read-only and temporary output is bounded. The generated image digest, service directory, command argv, resource limits, prepared commit, toolchain, build-plan version, and dependency hash are frozen into each run. When the deployed commit changes, Mendry rebuilds the enhanced profile; new enhanced runs are rejected while its provenance is stale. Basic Draft PR mode has no such image-provenance requirement.

## Enhanced-mode platform prerequisites

Only deployments that enable local pre-validation need Docker and builder configuration:

```dotenv
MENDRY_REMEDIATION_ARTIFACT_ROOT=/var/lib/mendry/artifacts
MENDRY_REMEDIATION_WORKSPACE_ROOT=/var/lib/mendry/workspaces
MENDRY_REMEDIATION_GO_BUILDER_IMAGE=registry.example/mendry/go@sha256:<64-lowercase-hex>
MENDRY_REMEDIATION_NODE_BUILDER_IMAGE=registry.example/mendry/node@sha256:<64-lowercase-hex>
# Optional executable overrides:
MENDRY_REMEDIATION_DOCKER_COMMAND=docker
MENDRY_REMEDIATION_GIT_COMMAND=git
```

Builder references are deployment inputs, never project settings, and mutable tags are rejected. Image preparation may use registry network access; patch validation does not. Prepared images must remain available for the lifetime of runs that reference them. Multi-host execution requires distributing those images to validation workers.

Preparation jobs are durable and bounded to two concurrent jobs per API process. Restart recovery re-resolves current repository and credential versions before rebuilding. A newer repository update replaces an older queued target, and stale workers cannot complete the replacement job.

## API

All endpoints require authenticated project access.

The normal remediation-policy endpoint accepts `auto_hotfix` when `validationProfile.enabled` is explicitly `false`. The policy still requires `resilient_v1`, a Git write credential, publication settings, and a bounded change policy. Clients cannot use this endpoint to inject an enhanced image or validation command.

Enhanced-mode setup uses:

- `POST /api/v1/projects/{projectKey}/configuration/auto-hotfix/check` with `{ "directory": "" }` to start preparation.
- `GET /api/v1/projects/{projectKey}/configuration/auto-hotfix/check` to poll `idle`, `checking`, `needs_selection`, `ready`, `enabling`, `enabled`, or `blocked`.
- `POST /api/v1/projects/{projectKey}/configuration/auto-hotfix/enable` with `{ "checkId": "..." }` to atomically commit the server-generated enhanced profile.

To repair an existing incident with the current policy, call `POST /api/v1/projects/{projectKey}/incidents/{id}/remediation/repair` with concurrency guards `{ "generation": 2, "expectedRunId": "...", "expectedVersion": 4 }`. The request cannot supply execution policy or validation commands. It creates a linked attempt, preserves prior history, re-diagnoses against the deployed baseline, and inherits no authorization to publish an old patch.
