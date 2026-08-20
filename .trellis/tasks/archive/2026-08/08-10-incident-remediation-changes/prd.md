# Incident Remediation Change Requests

## Goal

Publish a harness-validated incident repair as a transparent, human-governed
remediation package and restricted Git change, including a native Yunxiao draft
PR when that SCM provider is configured.

## Parent References

`08-10-production-incident-mvp/prd.md`, `design.md`, and `implement.md`.

## Dependencies

Requires `08-10-incident-service-foundation`,
`08-10-incident-domain-lifecycle`, and
`08-14-agentic-remediation-harness`. It consumes the harness's validated
diagnosis, selected plan, content-addressed patch, expected Git tree hash, test
results, authorization, audit, and idempotency contracts; it must not create a
parallel agent, LLM, sandbox, or background-job execution path.

## Confirmed Requirements

- Record a remediation package with cited evidence, diagnosis rationale,
  uncertainty, proposed patch, test results, risk, rollback plan, approval
  state, and verification criteria.
- Accept publication only for a harness result whose exact deployed baseline,
  selected plan, content-addressed patch/expected tree hash, required test
  results, risk class, and policy decision are complete and still valid. The
  publisher uses a fresh trusted checkout, applies the patch, and rejects a
  resulting tree-hash mismatch before commit or push.
- Use a separately scoped Git/SCM change credential restricted to configured
  repositories and policy-approved branch namespaces. Provider-neutral Git
  publication creates the already-validated hotfix branch, commit, and push for
  configured GitHub, GitLab, Yunxiao, Gitee, and generic remotes.
- Create a native draft PR for Yunxiao without operator approval. For other MVP
  providers, return the pushed branch plus available compare metadata so a user
  can create the change request manually.
- Require human approval to merge any resulting change into the production branch.
  Never deploy to production, execute production configuration or database
  changes, or claim recovery without independent verification.
- Make the complete chain visible in the console: evidence -> diagnosis ->
  rationale -> patch/test -> hotfix branch -> draft PR -> verification.
- Execute publication through the shared RabbitMQ job runtime. The job carries
  durable run/package/patch-artifact/repository references rather than mutable
  workspaces, source archives, or secrets; repeated delivery must resolve to the
  same branch, commit, and provider-specific change-request result.

## Out Of Scope

- PR merge, release/deployment execution, production rollback, and direct
  infrastructure or database writes.
- Unrestricted repository access, arbitrary shell commands on production, and
  uncited model-generated code.
- Agent diagnosis, repair-plan selection, repository editing, or test execution;
  those belong to `08-14-agentic-remediation-harness`.
- Native GitHub, GitLab, or Gitee pull/merge-request creation in the MVP.

## Disposition

Archived 2026-08-20. Publication remains a product gap, but this contract overlaps `08-14-agentic-remediation-harness` and depends on the cancelled RabbitMQ job runtime. The walking skeleton freezes no-Git-write. Reopen publication as a later harness slice after the diagnosis checkpoint.

## Acceptance Criteria

- [ ] A remediation package exposes its evidence citations, diagnostic reason,
      uncertainty, proposed patch, test output, risk/rollback, and
      verification criteria as separate reviewable fields.
- [ ] Publication rejects a missing, stale, policy-denied, unvalidated, or
      hash-mismatched harness result before any Git write.
- [ ] Git fakes prove configured GitHub, GitLab, Yunxiao, Gitee, and generic
      remotes create exactly one restricted branch/commit/push from the exact
      deployed commit; Yunxiao alone creates a native draft PR in the MVP while
      other providers return truthful branch/compare metadata.
- [ ] Repository credentials and API responses never expose secret values; an
      unconfigured repository, branch prefix, or baseline prevents a write.
- [ ] Tests prove no code path can merge, deploy, or perform a production
      configuration/database write.
- [ ] Worker interruption and RabbitMQ redelivery cannot create a second hotfix
      branch or draft PR for the same remediation/idempotency key.

## Approval Boundary

Committing and pushing a harness-validated restricted hotfix branch and opening
a Yunxiao draft PR are automated actions. Only a human may create other-provider
change requests and approve the final merge to the production branch;
deployment remains outside this system's authority.
