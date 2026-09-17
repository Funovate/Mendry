# Publication Release Inputs

## Status

**Pending external inputs.** This record distinguishes facts verified in the
repository from decisions and actions that must be supplied by a release owner.
It is not deployment, DNS, domain-ownership, indexing, or package-release
authorization.

The approved active brand is `Mendry`. The formal documentation origin
for the static public contract is `https://www.mendry.net`; documentation routes
remain `/docs/` and `/zh-cn/docs/`.

The owner-supplied canonical source repository is
[Funovate/Mendry](https://github.com/Funovate/Mendry). The production branch
and candidate release commit still need to be recorded. The local checkout's
remote configuration is separate from this supplied repository URL.

## Verified Repository Facts

| Area                             | Evidence                                                                                                 | Current result                                                                                                                                     |
| -------------------------------- | -------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Docs package                     | `docs/package.json`, `docs/package-lock.json`                                                            | Package name `@mendry/docs`, version `0.1.0`, Astro `7.3.1`, Starlight `0.42.0`                                                                    |
| Node line                        | `docs/.node-version`                                                                                     | `22.19.0`                                                                                                                                          |
| Pages build                      | `docs/README.md`, `docs/operations/publication.md`                                                       | Root `docs`, command `npm run build`, output `dist`                                                                                                |
| Preview behavior                 | `docs/astro.config.mjs`, `docs/scripts/verify.mjs`                                                       | Non-indexable preview with no canonical or sitemap; `robots.txt` disallows `/`                                                                     |
| Public contract                  | `docs/scripts/build-public.mjs`, `docs/scripts/public-origin.mjs`                                        | Static contract uses `DOCS_PUBLIC_RELEASE=true` and `PUBLIC_SITE_ORIGIN=https://www.mendry.net`                                                    |
| Backend/frontend version markers | `frontend/package.json`, `backend/go.mod`                                                                | Frontend package `mendry` version `0.1.0`; Go module `mendry/backend`                                                                              |
| Product installation status      | Paired `docs/src/content/docs/**/get-started/install.mdx` pages                                          | `planned`; no customer installation artifact is claimed                                                                                            |
| Security boundary                | `docs/src/content/docs/docs/concepts/security.mdx` and backend sources                                   | Product security/authority behavior is documented; this is not a substitute for an approved public security policy or support process              |
| Source control metadata          | `git remote -v`, `git tag --list`                                                                        | Both commands produced no entries in this checkout                                                                                                 |
| Repository release files         | Repository scan for top-level license, security/support policy, changelog, Dockerfile, and Compose files | No top-level `LICENSE`, `SECURITY.md`, `SUPPORT.md`, `CHANGELOG`, Dockerfile, or Compose manifest was found                                        |
| Brand assets                     | `docs/src/assets/brand/`                                                                                 | Directory is absent; current docs use text-only branding as required by the docs quality contract                                                  |
| Homepage media                   | `docs/src/components/Introduction.astro`, `docs/src/assets/media.yml`                                    | Static semantic execution model plus generated `public/media/execution-model.png`; no external runtime or 3D scene                                 |
| Provider readiness               | Paired signed-webhook guides                                                                             | AWS CloudWatch/SNS release verification is pending; Tencent CLS and OpenAI-compatible provider paths remain documented as preview where applicable |

The homepage execution model is rendered as local semantic HTML/CSS. The
previous vendored `hero-flow` Three.js runtime and generated fallback are no
longer part of the production docs surface; historical prototype files were archived outside the repository during removal of the development workflow tooling.

## Inputs Still Required

| Input                                 | What must be supplied or decided                                                                                                                                      | Why it blocks publication                                                                                      |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Domain ownership and Pages attachment | Confirm control of `www.mendry.net`, attach it to the approved Pages project, and record the production HTTP smoke result. No DNS or Pages action was performed here. | A build origin is not evidence that the domain resolves to the intended deployment.                            |
| Cloudflare project and access         | Record the Cloudflare account, Pages project, operator access, connected repository, and production branch.                                                           | The repository contains no provider account/project configuration; the source repository URL is now supplied.  |
| Production source selection           | Confirm the production branch in `https://github.com/Funovate/Mendry`, then record the candidate commit.                                                              | The canonical repository is supplied, but the production branch and candidate commit are not yet recorded.     |
| License and attribution               | Approve a repository license and any attribution obligations, including treatment of bundled/vendor media.                                                            | No repository license or legal publication decision is present.                                                |
| Authorized brand package              | Supply local logo/favicon files and explicit repository/public-site usage rights, or confirm text-only publication remains the approved choice.                       | The docs quality contract forbids inventing replacement brand assets.                                          |
| Release/version policy                | Decide the public version, tag format, changelog ownership, and release-commit policy for the `0.1.0` package markers.                                                | There are no tags or release policy in the repository.                                                         |
| Installation artifact                 | Supply a tested, versioned customer artifact plus supported platforms, upgrade procedure, rollback procedure, and verification evidence.                              | The installation pages must remain `planned` until this exists.                                                |
| Support and security statement        | Supply the public support channel/process, support status, security disclosure path, and any response boundary.                                                       | Existing technical security documentation does not state an external support or disclosure commitment.         |
| Provider release evidence             | Complete the AWS CloudWatch/SNS task and its checks before publishing the new bilingual provider procedure. Confirm any real provider/account smoke setup separately. | The docs currently overlap an `in_progress` task, and static docs checks do not prove live provider readiness. |
| Final publication approval            | Record owner approval for production indexing, domain activation, the candidate commit, and the publication record.                                                   | The static public build is explicitly not deployment authorization.                                            |

## Current Release Decision

Until the inputs above are recorded and approved:

- Keep `DOCS_PUBLIC_RELEASE` absent or not `true` for previews.
- Keep preview responses `noindex, nofollow`, with no canonical or sitemap.
- Use `npm run build:public` only as the static `https://www.mendry.net` contract
  test; it does not deploy, attach the domain, or enable indexing.
- Keep the installation page `planned` and do not add unsupported image, package,
  Compose, or shell instructions.
- Do not perform DNS, Cloudflare account changes, remote repository changes, or
  search-engine submission as part of this docs consolidation.

## Validation Record For This Pass

The pinned verification was run with Node `v22.19.0` and npm `10.9.3`:

```text
Command: npm --prefix docs run verify
Result: passed
- Prettier formatting: passed
- Astro/content checks: 0 errors, 0 warnings, 0 hints
- Route parity: 38 operator routes and 2 introductions
- Node contract tests: 14 passed, 0 failed
- Production dependency audit: 0 vulnerabilities
- Preview build: 40 routes and 41 generated pages; preview metadata verified
- Browser tests: 52 passed, 2 intentional mobile Pagefind skips
- Public build: canonical/locale/sitemap contract passed for https://www.mendry.net
- Final restoration: preview output restored; no canonical or sitemap remained
```

The first attempt stopped at the formatting check because this new file had not
been formatted yet; the verification script still restored the preview output.
Prettier was then run on this file and the complete command above passed. The
full command log was archived outside the repository during removal of the development workflow tooling.

See the [Cloudflare Pages publication runbook](./publication.md) for the
operational checklist and rollback procedure. The runbook links back to this
record so external inputs are not confused with the static build contract.
