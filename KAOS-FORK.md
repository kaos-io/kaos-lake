# KAOS fork of lakeFS

This repository is a KAOS-owned mirror of lakeFS, maintained on branch `kaos/1.86`.

## Origin

- Upstream: https://github.com/treeverse/lakeFS
- Tag: `v1.86.0`
- Upstream commit: `36fad6476e23c118b91247523a0c7fb045cead4e`

## Licence

lakeFS v1.86.0 is licensed under the Apache License, Version 2.0. The upstream
`LICENSE` and `NOTICE` files are kept unchanged in this repository. All KAOS
modifications are likewise distributed under Apache-2.0.

## Rule: no upstream code after v1.86.0

Starting with v1.87.0, upstream lakeFS is licensed under the Business Source
License 1.1 (BUSL-1.1). Therefore:

- Do **not** merge, rebase onto, or cherry-pick any upstream commit made after
  `v1.86.0`.
- Do **not** copy code from upstream releases or branches after `v1.86.0`.
- Security fixes and other changes must be written independently, or taken only
  from sources licensed under Apache-2.0 (or a compatible licence).

## Trademark

"lakeFS" is a trademark of Treeverse Ltd. Its use here only identifies the
origin of the code and does not imply endorsement by or affiliation with
Treeverse.

## Marking of modifications

In accordance with Apache-2.0 Section 4(b), every file modified by KAOS carries
a prominent notice stating that it was changed. KAOS changes are also recorded
in the Git history of this repository after the upstream `v1.86.0` commit.

The notice is a comment containing the exact text `Modified by KAOS` followed by a short
description of the change. `kaos/check-lineage.sh` enforces it, together with the rules above.

## Distribution: kaos-lake

The KAOS build of this repository is published as `kaos-lake` (PRD novelcore/kaos#1217).
"lakeFS" identifies the origin of the code only.

- `kaos/Dockerfile` builds the image, web UI included, with the same runtime layout as upstream.
- `kaos/VERSION` holds the release version (`1.86.0-kaos.N`).
- `kaos/check-lineage.sh` fails when the branch could contain upstream code after v1.86.0, when
  `LICENSE` or `NOTICE` changed, or when a modified upstream file lacks the notice.
- `kaos/test.sh` is the test gate.

Releases run on Argo Workflows (WorkflowTemplate `kaos-lake-build` in novelcore/kaos), never on
GitHub Actions. Every pull request merged into `kaos/1.86` runs the lineage check and tests, then
pushes an image to the platform OCI registry:

- a merge that changes `kaos/VERSION` publishes `kaos-lake:<VERSION>`;
- any other merge publishes `kaos-lake:<VERSION>-dev-<date>-<time>-<sha7>`.

To release, open a pull request that bumps `kaos/VERSION`.

## Changes from lakeFS v1.86.0

- **No phone-home by default.** `stats.enabled`, `security.check_latest_version` are `false`
  and `security.audit_check_url` is empty (the audit check is off). Each can be set again.
- **No Enterprise upsell in the web UI.** A repository has no Objects/Tables toggle, and the top
  bar's Datasets entry opens KAOS's own page. Upstream's Datasets and Tables pages only advertised
  lakeFS Enterprise ("Book a demo").
- **Datasets in the web UI** (PRD novelcore/kaos#1283 F-03/F-04/F-06, `webui/src/lib/kaos`,
  `webui/src/pages/kaos`). Repository cards carry a coloured purpose badge (Dataset, Default,
  Outputs · app, Orphaned, Other). The Datasets page lists `dataset-*` repositories with their
  description, layout and latest version, with Release version (tags `v1`, `v2`, …) and Check layout.
  New dataset creates one as the signed-in person through the regular API: with no layout (an empty
  dataset, the default, like `kaosctl dataset create`) or from a layout picked on purpose. Text files up to
  1 MiB have Edit; a yolo dataset's `data.yaml` opens as a class-list form. Saving opens a pull
  request into main from a draft branch, never a direct write (F-05).
- **Repositories with a purpose** (PRD novelcore/kaos#1283 F-01/F-02). `kaos.*` repository
  metadata marks a dataset (`dataset-*`) or an ML app's outputs (`outputs-<app>`). With
  `kaos.repositories.storage_namespace_prefix` set, the project's default dataset
  (`kaos.repositories.default_dataset`) and each declared app's `outputs_repository` are created
  and relabelled every `kaos.repositories.interval`; an undeclared outputs repository is labelled
  `kaos.orphaned` and never deleted. The metadata API refuses labels that break the naming rules.
  With `kaos.repositories.location` set, every repository under the prefix is labelled
  `kaos.location` (a platform key). `kaos.shared_with` (projects that may read a dataset) is
  validated and needs the delete-repository right, so only the project's admins share. Editors
  (Developers) may update `dataset-*` repositories so they can label the datasets they create.
  Each project named in a dataset's `kaos.shared_with` gets a group `kaos-consumer-<project>`
  with a read-only policy over exactly the datasets shared with it, removed when nothing is
  shared any more. With `kaos.token_identity.project` and `consumer_claim_prefixes` set, a token
  whose role claims name another project of the organisation joins that project's group while it
  exists (F-12). The web UI shows location and sharing badges and a Share form on each dataset.
- **Dataset layouts** (PRD novelcore/kaos#1283 F-04/F-05). A layout is a declarative spec
  (`layout.yaml`, schema `pkg/kaos/templates/shared/layout.schema.json`): name, title, description,
  version, parameters, a starter skeleton with `{{placeholders}}`, validation rules (required and
  allowed path patterns, extensions, pairs such as `images/<x>.jpg` ↔ `labels/<x>.txt`, line
  patterns, a schema for a YAML manifest), what a pull downloads, and an optional Python hook. The
  schema, the built-in layouts (`yolo`, `raw`) and the test fixtures are the same bytes as in kaos-lib
  (`src/kaos_lib/layouts/shared`, `tests/layout_fixtures`, pinned by `SHA256SUMS`), and both
  implementations pass the same fixtures. `pkg/kaos/templates` serves the built-in layouts and the
  project's own: `<name>/layout.yaml` on main of the project's lakeFS repository `layouts`, which
  people who may create datasets write (`kaos-cli layout publish` opens a pull request), so a project
  adds a layout without a platform release; a project layout that is not valid, or takes a built-in's
  name, is left out and reported. `GET /kaos/v1/dataset-templates` lists them with `source`
  (`builtin`/`project`) and the whole spec, `POST /kaos/v1/dataset-templates/{name}/render` (form
  values → a new dataset's files), `.../parse` (a dataset's form file → form values, for editing) and
  `.../validate` (`{repository, ref}` → the problems of the layout's declarative rules, for a caller
  who may list and read the repository; up to 200 000 files, content of at most 5 000 files and
  64 MiB). kaos-lake never runs a layout's hook: only kaos-lib does. These endpoints need a signed-in
  caller and write nothing: the UI and the CLI commit the files with the caller's own identity. New
  datasets are labelled `kaos.template=<layout>`; ids datasets carried before a layout's rename read
  as the layout's current name and are never written.
- **Dataset catalog** (PRD novelcore/kaos#1283 F-07, D-05). With workload identity on and
  `kaos.catalog.namespaces` set, `GET /kaos/v1/catalog` answers the datasets (name, default,
  description, location, main's head, and versions: `vN` newest first, then every other tag by name
  as a named version, never the `k-*` tags of kept runs, PRD novelcore/kaos#1911 D-03) to pods of those namespaces by
  their service-account token, any account, acting as no user. It never serves data.
- **Dataset release event** (PRD novelcore/kaos#1283 D-05). With `kaos.dataset_events.namespace` set
  (the project's CI namespace), a created or deleted version tag on a `dataset-*` repository (`vN`, or
  any other name since PRD novelcore/kaos#1911 D-03; never a kept run's `k-*`), a repository
  labelled `kaos.kind=dataset`, and a deleted `dataset-*` repository make kaos-lake create ONE Workflow
  there (`ml-ci-dataset-release`, as `dataset-release-sa`), through the Kubernetes API with its own
  service-account token, which asks each of the project's ML apps to re-read the catalog and re-render
  its pipeline's `dataset` dropdown. Changes inside a 10 s window are one Workflow; one is also
  created at start, so a change lost while the server was down is caught up. Every path that creates a
  tag goes through the API (`kaosctl dataset release`, the UI, the seeder), so the hooks are in the
  controller (`pkg/api/controller.go`: CreateTag, DeleteTag, DeleteRepository, SetRepositoryMetadata) and
  in the reconcile pass that labels a hand-made dataset (`pkg/kaos/repos`). A refused or lost event is
  not an error: the hourly reconcile compares the datasets each pipeline was rendered with against
  the catalog. Datasets shared by other projects change a consumer's catalog too, but their events stay
  in the owner's project: the consumer's backstop picks them up. Code: `pkg/kaos/datasetevents`.
- **Multi-user auth mode** (PRD novelcore/kaos#1217 F-02). `auth.ui_config.rbac: internal` with no
  `auth.api.endpoint` runs the v1.86 key-value auth store (`contrib/auth/acl`) inside the server, in
  lakeFS's own database: users, groups and policies through the standard auth API and UI, evaluated
  by the unchanged core. Code: `pkg/auth/multiuser`. Every other `rbac` value behaves as upstream.
  - Setup creates upstream's RBAC base groups (Admins, SuperUsers, Developers, Viewers) and policies.
  - A server switched from `rbac: none` migrates on its first start: the basic-mode admin keeps its
    name and key pair and joins Admins. Basic-mode records are not changed, so switching back to
    `none` keeps working with the admin key; users created in multi-user mode exist only there.
- **Encryption key rotation** (PRD novelcore/kaos#1217 T-07). `auth.encrypt.previous_secret_key`
  (`LAKEFS_AUTH_ENCRYPT_PREVIOUS_SECRET_KEY`) names the key secrets were stored under before. On
  start, `lakefs run` re-encrypts with `auth.encrypt.secret_key` every stored access-key secret that
  only the previous key opens, in both auth modes, then serves. Access keys do not change. A secret
  neither key opens stops the start. Code: `pkg/auth/rekey`. Used to move existing stacks off the
  lakeFS Helm chart's built-in default key.
- **Declared app users** (PRD novelcore/kaos#1217 F-06). In multi-user mode, `kaos.app_users.path`
  names a directory with one JSON file per ML app (`user`, `access_key_id`, `secret_access_key`,
  and optionally `namespace`, see workload identity),
  normally a mounted Secret. On start and every `kaos.app_users.interval` (default 30s) the server
  makes the store match it: each `kaos-app-*` user exists in Developers with exactly its declared
  key, undeclared `kaos-app-*` users are deleted with their keys, and other users are never
  touched. Developers also get `KAOSRepositoryCreate` (create repositories; deleting stays with
  Admins). An invalid file changes nothing. Code: `pkg/auth/declared`.
- **Token identity** (PRD novelcore/kaos#1217 F-03). In multi-user mode, `kaos.token_identity`
  (`issuer`, `audiences`, `groups_claim` default `groups`, and `groups`: a list of `claim` →
  `group`) accepts that identity provider's access tokens as `Authorization: Bearer`. The token
  is verified against the issuer's published keys (OIDC discovery, RS256 only, issuer, audience,
  expiry required). The caller is the lakeFS user named by the token's `email` claim (it must
  contain `@`, so a token can never act as a platform or app user), created on first use, and its
  groups are set to exactly the mapped values of the groups claim on every call. A token that
  cannot be verified, or maps to no group, is denied; nothing falls back to another identity.
  lakeFS's own login tokens are unchanged. Code: `pkg/auth/tokenid`; hook in
  `pkg/api/auth_middleware.go`.
- **Browser sign-in** (PRD novelcore/kaos#1217 F-04). With token identity on, `kaos.browser_login`
  (`client_id`, `client_secret`, `redirect_url`, `scopes`) adds `/oidc/login` and `/oidc/callback`:
  authorization code with PKCE (S256) and a single-use state cookie, then the ID token (audienced
  to that client, carrying the email and groups claims) goes through the same verification and
  group mapping as a bearer token, and the same lakeFS session `/auth/login` creates is started.
  Set `auth.ui_config.fallback_login_url: /oidc/login` for the UI's sign-in button. A person with
  no mapped role gets a 403 page saying so; nothing falls back to the admin key.
- **Workload identity** (PRD novelcore/kaos#1217 F-05). With declared app users on,
  `kaos.workload_identity` (`audience`, `issuer`, `key_ttl` default 1h) accepts the Kubernetes
  service-account tokens of ML workflow pods. A token of service account `{app}-workflow`,
  carrying `audience`, acts as the declared user `kaos-app-{app}` when it comes from the
  `namespace` declared for that app (the user must exist); every other token is denied, as is
  every token of an app declared without a namespace. `issuer` defaults to the issuer of
  the server's own service-account token, the cluster it runs in; keys come from its OIDC
  discovery as for token identity. Such a token works as `Authorization: Bearer`, and
  `POST /kaos/v1/credentials` with it returns a new access key of that user (`access_key_id`,
  `secret_access_key`, `expiration`) for S3 clients. Exchanged keys start with `KAOSX`, are refused
  once `key_ttl` old, and are deleted by the declared-user reconcile after that. Code:
  `pkg/auth/tokenid/workload.go`, `pkg/auth/declared/exchange.go`.
- **Dependency security fixes** (PRD novelcore/kaos#1217 F-09). Go modules raised to versions
  that fix their CVEs: kin-openapi v0.149.0 (2 critical), thrift v0.24.0, grpc v1.83.2,
  golang.org/x/{crypto,net,text,mod}, go-jose v4.1.6, klauspost/compress v1.18.7. kin-openapi after
  v0.131 reads the whole request body even for operations excluded from body validation, which
  buffered every upload in memory (1 GiB object: 2.8 GB RSS); `pkg/api/serve.go` gives the
  validator a body-less copy of such requests, so uploads stream as before (1 GiB: ~120 MB).
  `go.sum` is exempt from the modification-notice check (generated; `go.mod` carries the notice).
- **Per-app rights** (PRD novelcore/kaos#1283 F-08, D-04). Declared app users no longer join
  Developers. Each `kaos-app-<app>` has its own policy `KAOSApp-<app>`: read (`fs:Read*`,
  `fs:List*`) on every `dataset-*`; read, write, commit, branch and tag on its own
  `outputs_repository` (never repository deletion or settings); and read/write on
  `kaos.repositories.legacy_repository`, the project repository apps created before outputs
  repositories still write to (empty: none). The reconcile creates, updates and attaches the
  policy, detaches anything else from the user, and deletes `KAOSApp-*` policies of apps that are
  no longer declared. Users without the `kaos-app-` prefix are never touched. Code:
  `pkg/auth/declared/policy.go`.
- **MeluXina staging view** (PRD novelcore/kaos#1283 F-10, D-08). With workload identity on and
  `kaos.meluxina.namespaces` set, pods of those namespaces `POST /kaos/v1/meluxina/snapshot` the
  stager's report by service-account token: `taken_at`, `space` (`used_bytes`, `limit_bytes`,
  `free_bytes`, `used_files`, `limit_files`; null = unlimited or unreadable), `staged` (`repo`,
  `commit`, `ref`, `files`, `bytes`, `staged_at`, `last_used`, `leased`; null allowed except
  repo and commit), `retention`, `last_error`. Bodies over 2 MiB, more than 5000 staged versions
  and malformed values are refused with the rule named. A failed run (an error and no data) keeps
  the previous good `staged`/`space`/`retention` and records only `last_error` and `taken_at`;
  `last_success_at` (set by the server) is the time of the last good data. The latest is kept in
  memory and in the key-value store, and `GET /kaos/v1/meluxina` returns it, with `server_time`,
  to any signed-in user. The Datasets page has a MeluXina tab: staged versions per dataset (tag
  names from the dataset's tags, else "untagged"), times, files, size, in-use badge, space bar,
  retention rule, and alerts (free under 10%, last good data older than 30 minutes, last error).
  Code: `pkg/kaos/meluxina`, `webui/src/pages/kaos/meluxina.jsx`.
- **Dataset UX** (PRD novelcore/kaos#1283 F-03/F-07). Each dataset has its own page
  (`/datasets/<repo>`) and a "How to use in a pipeline" panel with the exact
  `dataset = <repo>@<version>` string and a copy button per version, with links to the version's
  data, commit and tags; "Open pull requests" per dataset; after New dataset the dataset page shows
  next steps (add data through a branch and pull request or `kubecore dataset push`, then release a
  version); after Edit the dialog links to the pull request; repository pages of datasets and
  outputs show a strip. `kaos.ui.console_url` adds a link back to the KAOS console in the top bar
  (`GET /kaos/v1/ui`).
- **An outputs repository is its app's Model** (PRD novelcore/kaos#1283). Its badge reads
  `Model · <app>`, and its strip is the Model panel: links to the console's Models area
  (`<console_url>/models`) and to MLflow (`<mlflow_url>/#/models`, set by `kaos.ui.mlflow_url`,
  an absolute http(s) URL, else no link; `GET /kaos/v1/ui` answers `mlflow_url`); the registered
  versions (tags `model-<name>-v<N>`, newest first per model, up to 100) with the tagged commit's
  experiment, run and dataset, its files under `checkpoints/<experiment>/<run>/` and its MLflow run
  (`#/runs/<mlflow_run_id>`); and the last 20 training runs (commits on main, first parents of the
  last 100, with `kind` `checkpoints` or `quantization`), each marked with the tag it is registered
  as. Code: `webui/src/lib/kaos/model.js`, `webui/src/lib/kaos/modelPanel.jsx`.
- **Unknown `kaos.*` keys** do not stop the server: config is decoded exactly, but the `kaos`
  block collects keys it does not know instead of failing, so a newer chart cannot break an older
  image.
- **CORS for the KAOS console** (PRD novelcore/kaos#1283). `kaos.ui.cors_origins` lists exact
  origins (`https://<org>.console.<domain>`); empty: off. For those origins only, `/api/v1/` answers
  a GET preflight with 204 without authentication (`Allow-Origin` echoed, never `*`, methods
  `GET, OPTIONS`, headers `Authorization, Content-Type, Accept`, max age 600) and adds
  `Access-Control-Allow-Origin` and `Vary: Origin` to GET responses. Any other method or origin gets
  no CORS headers, and `Access-Control-Allow-Credentials` is never sent, so the session cookie is
  unusable cross-origin. Code: `pkg/kaos/cors`. lakeFS 1.86's repository list does not carry
  repository metadata (the `Repository` schema has no such field), so clients read
  `/repositories/{repo}/metadata` per repository.
- **A dataset's main changes only through pull requests** (PRD novelcore/kaos#1283). The
  repositories reconcile gives `main` of every repository labelled `kaos.kind=dataset` lakeFS's
  branch protection rule blocking `staging_write` and `commit`; a merge still lands. It keys off the
  label (set last by the New dataset flow, after the first commit to main, so that commit is not
  blocked), adds only what is missing, re-applies a deleted rule on the next pass (every
  `kaos.repositories.interval`), and never removes protection, so a dataset that loses its label
  stays protected. Outputs repositories are not protected. Editors (Developers) lack
  `branches:SetBranchProtectionRules`, so only Admins change rules. Between a dataset's label and the
  next pass main is open for up to one interval. Code: `pkg/kaos/repos/protect.go`.
- **Templates are optional: a `dataset-*` repository made by hand is a dataset** (PRD
  novelcore/kaos#1283, owner 2026-11-01). The repositories reconcile labels `kaos.kind=dataset` on
  any validly named `dataset-*` repository without a kind, made with lakeFS's own Create repository,
  the API or the kubecore package, once its default branch has a commit of its own. So, as with
  the New dataset flow, the first upload straight to main goes through, and only then does the
  repository show on the Datasets page and in the pipeline dropdown and get main's protection.
  People's own labels stay; it never sets `kaos.template`. Code: `pkg/kaos/repos/repos.go`.
- **Datasets shared across projects, for pipelines** (PRD novelcore/kaos#1283 F-12, release
  `1.86.0-kaos.18`). Owner side: `GET /kaos/v1/shared` (with token identity on, behind the API's
  authentication) answers `{"owner": <kaos.token_identity.project>, "datasets": [...]}`: the
  `dataset-*` repositories labelled `kaos.kind=dataset` whose `kaos.shared_with` names a project of
  one of the caller's `kaos-consumer-<project>` groups, never the default flag nor who else they
  are shared with. Only consumer groups count: a caller without one (this project's own people,
  its pipelines, anyone else) gets 401. A metadata write or delete touching `kaos.shared_with`
  runs the shares reconcile at once (`repos.Kick`). Consumer side: the top-level block
  `kaos.sharing` (`peers_path`, `machine_key_path`, `interval` default 1m, `cache_ttl` default
  60s; unknown sub-keys ignored; no block: off, nothing changes) names the peers file
  `{"project":"B","peers":[{"project":"A","endpoint":"…","url":"…"}]}`, re-read every interval
  (missing: no peers; not JSON: the last good copy stays; invalid entries and the own project
  left out). `GET /kaos/v1/catalog` then lists, after the project's own datasets, each peer's
  `/kaos/v1/shared` named `<owner>/<dataset>` with `owner` set and never `default`: peers asked in
  parallel, 5 s each, answers reused for `cache_ttl`, a failing peer's last good answer served for
  up to 10 minutes, 401 read as nothing shared, 404 or an HTML page as an older peer. The token
  sent to peers is minted from this project's Zitadel machine key (JWT profile, RS256, at
  `{kaos.token_identity.issuer}/oauth/v2/token`, audienced to `kaos.token_identity.audiences[0]`),
  reused until half its lifetime, the key re-read on each mint; access keys never leave.
  `GET /kaos/v1/peers` answers the peers file (empty without `kaos.sharing`) to any signed-in
  caller. Code: `pkg/kaos/sharing`, `pkg/kaos/repos/kick.go`.
- **"Shared with this project" on the Datasets page** (PRD novelcore/kaos#1283 F-12, release
  `1.86.0-kaos.20`). `GET /kaos/v1/shared-with-us`, behind the API's authentication, answers
  `{"project": <own>, "datasets": [{name: "<owner>/<repo>", owner, repo, location, description,
  template, latest, versions: [{tag, commit}], url: "<owner's public lakeFS url>/repositories/<repo>/objects"}],
  "notes": [{owner, note}]}` from the same aggregation and cache as `/kaos/v1/catalog`
  (`Aggregator.Collect`; the catalog's `Shared` flattens it), so the page never adds requests to
  peers. An owner that is down, too old, or served from its stale copy gets a note instead of an
  error. Without `kaos.sharing` it answers no datasets and no notes. Any signed-in person or app key
  of this lakeFS may call it, by design: what is shared with the project is shared with its people
  and pipelines, and the answer carries names, versions and links only, never the token sent to
  peers, their in-cluster endpoints, their errors, or who else a dataset is shared with. The
  catalog's datasets now carry `template` (`kaos.template`). The web UI lists them read-only below
  the project's own datasets with a "Shared by <owner>" badge, the copyable `dataset = <owner>/<repo>@vN`
  and a link to the owner's lakeFS; the section is hidden when nothing is shared, and an older
  server (404 or the UI's page) reads as nothing shared. Code: `pkg/kaos/sharing/handlers.go`,
  `webui/src/pages/kaos/sharedWithUs.jsx`.
- **A deleted repository leaves the repository cache** (release `1.86.0-kaos.28`). lakeFS caches
  each repository record (`graveler.repository_cache`, 5 s + 2 s jitter) and deleting a repository
  never invalidated that entry, so for 5-7 s after a delete `GET /repositories/<name>` answered 200
  with the dead record: recreating the same name failed ("already exists"), and metadata set on the
  recreated repository went to the dead instance's partition (`<id>-<InstanceUID>`), silently
  losing its `kaos.*` labels. `cache.Cache` gained `Delete(k)` (LRU `Remove`; no-op for `NoCache`),
  and `pkg/graveler/ref/manager.go` calls `repoCache.Delete(id)` after every write of the
  repository record: the move to `IN_DELETION`, the final delete, and creation. In-process only,
  which is enough for the single replica each project runs; with several replicas the other
  replicas stay bounded by the 5-7 s TTL. Marked `KAOS-FORK` in `pkg/cache` and `pkg/graveler/ref`.
- **Datasets a pipeline produces** (PRD novelcore/kaos#1911 F-10, D-04, CON-02). A declared app
  user's file may carry `datasets`: the `dataset-*` repositories its pipeline produces, by full
  repository name. A name that is not a dataset's, or a dataset declared twice (by two apps or by
  one), fails the whole load, so a dataset has one producer. For each, the app's `KAOSApp-<app>`
  policy adds, to the read every app has on every dataset: write and delete on objects; create,
  delete and commit on branches `run-*`; commit on `main`; create on tags `k-*`. Nothing on a
  dataset it does not declare, no other branch or tag (so no `vN` and no named version), no tag
  deletion, no branch reset, no repository settings. The repositories reconcile creates each
  declared dataset under `kaos.repositories.storage_namespace_prefix`, labels it
  `kaos.kind=dataset` and `kaos.producer=<app>`, and announces it at once (the dataset release
  event), so pipelines can pick it before its first run. It is labelled in the pass that creates
  it, so `main` is protected from the start: the commit right on `main` only ever lands a merge,
  and lakeFS refuses a direct write or commit there. `kaos.producer`, and every key under
  `kaos.producer.`, is a platform key: the metadata API refuses to set or delete it for any
  caller. A dataset that already exists is adopted and labelled; one no app declares any longer
  keeps its label and its data, and its former producer is back to read-only. lakeFS authorises
  object writes per repository, not per branch: on a branch of its dataset that is neither `main`
  nor `run-*` (one a person made) the producer can stage objects but never commit them. Code:
  `pkg/auth/declared`, `pkg/kaos/repos`.
