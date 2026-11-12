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
  `kaos.orphaned` and never deleted. The default dataset is given a small synthetic sample once
  (`README.txt`, `sample/measurements.csv`, `sample/notes-1.txt`, `sample/notes-2.txt`, under 100
  KB, embedded in `pkg/kaos/repos/sample`), so an app made from the starter template runs as it is:
  only while `main` still is the initial commit, holds no object and no branch but a crashed
  attempt's `kaos-seed-sample` (removed only when it provably is the seed's: it starts at that
  commit and holds nothing but sample files with the sample's size and checksum; any other branch of
  that name leaves the dataset without the sample, logged once); never a produced dataset. It is staged on that branch and merged
  into `main` with the expected head (squash), so it lands with protection on and two replicas
  leave one commit (message `kaos: sample data so a new project's default dataset runs as it is`,
  commit metadata `kaos.seed=sample-v1`). Code: `pkg/kaos/repos/seed.go`. The metadata API refuses labels that break the naming rules.
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
  stays protected. One dataset whose rules cannot be read or written does not stop the pass: the
  others are protected and every failure is logged. Outputs repositories are not protected. Editors (Developers) lack
  `branches:SetBranchProtectionRules`, so only Admins change rules. Between a dataset's label and the
  next pass main is open for up to one interval. Code: `pkg/kaos/repos/protect.go`.
- **Leftover run branches are removed** (PRD novelcore/kaos#1911 CON-03). A pipeline step writes on
  a `run-*` branch and deletes it when it finishes or fails in a way it catches; a killed pod or a
  cancelled run leaves the branch and the objects staged on it. A pass of the retention loop
  deletes the `run-*` branches of datasets labelled `kaos.producer` that nothing has written to for
  `kaos.repositories.run_branch_ttl` (default 168h; 0: never; otherwise at least 24h, the config
  refuses less: a living run branch is one whose upload is in progress). A branch's age is the
  newest of its own head commit and its uncommitted objects, so a step that keeps staging keeps its
  branch; lakeFS keeps no creation time for a branch and a new branch's head is `main`'s, however
  old, so a branch with neither has no age and is kept. `main`, other branches and other
  repositories are never touched; at most 200 branches go from a repository per pass; a failed
  delete is logged and does not stop the pass. A client that writes on a deleted branch gets
  not found. The log names the repository, the branch, the commit it was at, the age and the rule.
  Code: `pkg/kaos/repos/retention.go`.
- **Versions nobody promoted expire** (PRD novelcore/kaos#1911 CON-03). Each run's version is kept
  by a tag `k-<YYYYMMDDTHHMMSSZ>-<run>-<step>-<commit12>` (UTC time of sealing; the old form
  `k-<run>-<step>-<commit12>` is as old as its commit). A pass of the retention loop deletes, from
  datasets labelled `kaos.producer`, the `k-*` tags older than `kaos.repositories.keep_days` days
  by the time in their name (default 30; 0: never; set per project by the project's lake
  configuration), except a tag whose commit `main` holds (reachable from it, with the content of
  one of its commits, or named by the `kaos.promoted` metadata of one of its commits: the full ID of
  the sealed commit a promotion merged) and a tag whose commit another tag that is not a `k-*` tag
  points to (`latest`, a model, a name someone gave). Only the tag goes; no object is deleted. No
  commit is read for a tag whose name says it is not old; `main`'s history is read once, in one
  call for up to 10000 commits, and only when an expired tag exists; per repository and pass at
  most 200 tags are deleted and 500 commits read, the rest follows at the next pass. The log names
  the repository, the tag, the commit, the age and the rule.
- **Retention runs on its own schedule.** The two passes above run every
  `kaos.repositories.retention_interval` (default 1h, at least 5m), on their own, so that they never
  delay creating and protecting repositories.
- **A merge may name the head it expects** (PRD novelcore/kaos#1911 F-12, D-05, CON-03).
  `POST /repositories/{repository}/refs/{sourceRef}/merge/{destinationBranch}` takes an optional
  `expected_head`: the full ID of the commit the destination branch must be at. The comparison is
  made inside the branch's conditional update (`graveler.Merge`, on the branch record the write is
  conditional on), so of two merges racing with the same expected head exactly one lands; the
  other is retried by the core against the moved branch and refused there. A refusal changes
  nothing and answers 412 with a message naming the destination's current head, so the caller can
  restage from it. A branch name or a short ID is never a head: 412. Without the field, or with an
  empty one, a merge behaves as upstream, except into `main` of a dataset a pipeline produces (a
  repository labelled `kaos.producer`): there the field is required for every caller, and a merge
  without it is refused with 400, naming the field, so two runs cannot be mixed by a client that
  leaves it out. The first promotion into a fresh dataset names `main`'s initial commit. Merges
  into other branches, into datasets people upload and into outputs repositories are unchanged.
  When `main` of a produced dataset holds uncommitted objects (possible only after an admin
  removed its protection), a merge into it is refused with 400 saying that it holds uncommitted
  objects no run wrote, that the platform clears them within a minute, and to try again, instead
  of lakeFS's bare "dirty branch".
  A pull request carries no expected head, so merging one into `main` of a produced dataset is
  refused with 400 and the pull request stays open; pull requests elsewhere merge as before. Only the Go API code
  (`pkg/api/apigen`) is regenerated; the bundled SDK clients (`clients/`) are upstream's and do not
  know the field. Code: `api/swagger.yml`, `pkg/api/controller.go` (MergeIntoBranch),
  `pkg/graveler/graveler.go` (`WithExpectedHead`).
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
- **A branch protected from commits refuses every commit** (PRD novelcore/kaos#1911 CON-03).
  Upstream asks the branch protection rule `commit` only on a direct commit and a hard reset. A
  revert, a cherry-pick and an import each put a new commit at the branch's head as well, so
  `graveler.Revert`, `graveler.CherryPick` and `graveler.Import` now ask the same rule first and
  answer the same error (`cannot commit to protected branch`, 403), in every repository. With the
  rule KAOS sets on a dataset's `main`, that branch moves only by a merge (a pull request's or a
  promotion's); reverting a dataset's `main` is done on a branch and merged. Code:
  `pkg/graveler/graveler.go` (`refuseCommitToProtected`).
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
- **Datasets a pipeline produces** (PRD novelcore/kaos#1911 F-10, D-04, CON-02, CON-03). A dataset
  is written by people or by exactly one step of one pipeline, never both.
  - *Declaring.* A declared app user's file may carry `datasets`: the `dataset-*` repositories its
    pipeline produces, by full repository name. A name that is not a dataset's, or a dataset
    declared twice (by two apps or by one), fails the whole load.
  - *Creating.* The repositories reconcile creates each declared dataset that does not exist under
    `kaos.repositories.storage_namespace_prefix`, with `kaos.kind=dataset` and
    `kaos.producer=<app>` already on it: the labels are written before the record that makes the
    repository exist (`CreateRepositoryWithMetadata`, `pkg/graveler/ref/manager.go`), so it is
    never seen without them. Upstream creates a repository and its metadata in separate calls. The
    same pass protects its `main` and announces it (the dataset event), so pipelines can pick it
    before its first run. It is not created at once: people are kept out first (see *People*), so
    the repository appears about half a minute after the declaration with the default settings,
    and each pass until then logs `<name> is declared and not created yet: … it is created after
    <time>`. The API refuses to create a repository whose name an app declares, for
    every caller (400, `<name> is a dataset produced by <app>: the platform creates it`), so
    nobody can make it first, while the declarations are loaded. When they are not (for example
    another app's file is half-written: the whole load is skipped, `pkg/auth/declared`), the name
    is not declared and a person can create a repository with it. That repository is then never
    adopted (see *Never adopting*): the app has no write access to it until an admin removes the
    person's repository, and the repositories reconcile logs `Declared repositories not applied`
    on every pass until then.
  - *Never adopting.* A declared dataset that already exists is the app's only if it carries
    `kaos.producer=<that app>`. Every other existing repository is left exactly as it is, whatever
    it holds or does not hold: the project's default dataset, a dataset people uploaded, another
    app's dataset, and a repository without labels (even an empty one: the platform did not make
    it). There is no exception. The reconcile logs `dataset not adopted: app <app> declares it,
    but <reason>` every pass, the app gets no write access to it, and the other datasets and apps
    go on. A changed or removed producer label is not put back.
  - *The app's rights.* On a declared dataset the app's `KAOSApp-<app>` policy adds, to the read
    every app has on every dataset: write and delete on objects; create, delete and commit on
    branches `run-*`; commit on `main`; create on tags `k-*`. Nothing on a dataset it does not
    declare, no other branch or tag (so no `vN` and no named version), no tag deletion, no branch
    reset, no repository settings. These statements are written only while the dataset is ready:
    the repository exists, carries `kaos.producer=<app>`, its stored rules block `staging_write`
    and `commit` on `main`, and the write paths already enforce that (they read the rules through
    a cache a few seconds behind; `repos.ProducedBy` asks both, on every pass of the app-users
    reconcile). Until then the app only reads it; and when it stops being true, or the app no
    longer declares the dataset, the app is back to reading within one pass plus the policy cache
    lifetime (about 20 s by default): its policy is rewritten in that pass, and a session keeps the
    policies it read for `auth.cache.ttl` plus `auth.cache.jitter`.
    The two reconciles run on their own intervals; in either order the app is not given the commit
    right on a `main` that is not its own or not yet protected. So the commit right on `main`
    lands only a merge. That needs both parts of this change, the dataset rights and
    the merge that names `expected_head`; no app may declare a dataset on a build without the
    second. That rests on two things: protection enforced before the right is given,
    and "A branch protected from commits refuses every commit" above, without which a cherry-pick,
    a revert or an import would pass (upstream checks only a direct commit and a hard reset).
    One case is outside it, and only an admin can cause it: an admin who removes the protection
    rule of a produced dataset's `main` leaves the app, which already holds its rights, able to
    upload straight to `main` and to commit there until the next app-users pass takes the rights
    back and the next repositories pass restores the rule (one interval each, plus the policy
    cache lifetime for the rights). Objects uploaded to `main` and not committed make every
    promotion fail until they are gone (the merge answers 400 saying so, see "A merge may name the
    head it expects") and nobody can reset a protected branch through the API, so the platform
    clears them: see *Strays*.
  - *People.* The repositories reconcile keeps the policy `KAOSProducedDatasets` listing every
    repository labelled `kaos.producer` and attached to SuperUsers, Developers and Viewers. On
    those repositories it denies writing and deleting objects, every branch change (create,
    delete, commit and so merge, cherry-pick and import, revert and reset), opening and changing
    pull requests (one could never merge there: the rule "`main` of a produced dataset moves only
    by a merge that names `expected_head`" needs both parts of this change, the dataset rights and
    the expected-head merge, and no app may declare a dataset on a build without the second), creating and deleting `k-*` tags, commit
    records, and deleting the repository (SuperUsers hold `fs:*`, so without this denial one of
    them deletes a produced dataset and every sealed version with it; Admins still can); in lakeFS a denial wins over any allowance. People keep reading, describing the
    dataset (labels that are not the platform's), and naming versions: creating and deleting tags
    that are not `k-*` on commits that exist. A numbered release is not a name: a `vN` tag on a
    produced dataset is refused for every caller (400; such a dataset has commits, `latest` and
    names, PRD D-03), and so is a tag named `latest` or `main`, or made only of hexadecimal characters
    (`^[0-9a-fA-F]{4,64}$`): lakeFS resolves a tag before a commit prefix, so such a tag could
    shadow a sealed commit or a moving name (400, the name is reserved; name a version with
    letters that cannot be mistaken for a commit id). The Datasets page does not offer Release
    version there. Admins are not restricted: an admin can detach any policy and change any rule,
    so a denial there would only look like a guarantee, and repairing a dataset needs someone who
    can write it. An Admin who is also in Developers, SuperUsers or Viewers is denied like any
    member of those groups, because a denial wins: to repair a dataset, use an account that is in
    Admins only. Groups other
    than those three (a custom group mapped from a token claim) are not covered.
    A session keeps the policies it read for the auth cache's lifetime (`auth.cache.ttl` 20 s plus
    `auth.cache.jitter` 3 s by default, per process), so a rule written when a dataset appears
    would reach an editor with a session open about 20 s too late. The order prevents that, and
    holds with several replicas: the policy names repositories by name, so the reconcile lists a
    declared dataset in it before the repository exists, and creates the repository only once the
    policy has not changed for longer than that lifetime plus a 10 s margin
    (`repos.SettleMargin`). "Since when" is the policy's own timestamp, stored with it in the auth
    store: a restart or another replica reads the same value, nothing is kept in process memory.
    Any change to the list, or a group getting the policy back, restarts the wait for the datasets
    not created yet. A declared name whose repository exists without the label is not listed: it
    is someone else's. If the policy cannot be written, no produced dataset is created.
  - *Lakefs actions.* A lakeFS action (hook, read from `_lakefs_actions/` of a commit) runs with
    the rights of whoever triggers it, for example a person naming a version. An app could commit
    one on a run branch and promote it by merge, and it would later run with a person's rights
    and write datasets the app may not. So every `KAOSApp-<app>` policy denies writing and
    deleting objects under `_lakefs_actions/*` in every repository (its produced datasets and its
    outputs repository): upload, delete, copy into the prefix, stage, link and multipart all ask
    for those two actions on the destination object, and a denial wins. Reading it and every
    other path, `_lakefs_actions-notes/` included, are unchanged. That closes the app side only.
    The platform should also switch lakeFS actions off in the lake's configuration
    (`actions.enabled`, default `true` in `pkg/config/defaults.go`; the default is not changed in
    the fork), since actions committed by anyone else, or before this rule, still run.
  - *Strays.* Nothing stages on `main` of a produced dataset, so an uncommitted object there is not
    a run's (it can only get there while an admin has the protection off). Every pass, the
    repositories reconcile discards the uncommitted changes on `main` of each repository labelled
    `kaos.producer`, through a path of its own that the protection does not stop
    (`DiscardUncommitted`; no API route calls it), and logs a warning with the dataset, the count
    and up to 20 object names. It never touches committed data, another branch, or a repository
    without the label; a repository whose uncommitted changes cannot be read is left alone and
    reported.
  - *The label.* `kaos.producer`, and every key under `kaos.producer.`, is a platform key: the
    metadata API refuses to set or delete it for every caller, admins included. There is no route
    in this image that changes a dataset's producer or hands a produced dataset to people; it is
    not a side effect of changing an app's file either (the new app is refused, see above). Moving
    a dataset that holds data to another producer needs a platform change that does not exist yet.
    A dataset no app declares any longer keeps its label and its data, stays closed to people, and
    its former producer is back to read-only within one pass plus the policy cache lifetime (about
    20 s by default); the same app declaring it again gets it back.
  - *Reserved tag prefix.* `k-` is reserved, in every dataset, for the tags of kept runs: such a
    tag is never listed as a version and never fires the dataset event. Nothing in kaos-lake
    creates named versions (the UI's Release version creates `vN` only), so a person can create a
    `k-` tag on an uploaded dataset only through lakeFS's own tag API, where it is accepted and
    simply is not a version.
  - *A limit.* lakeFS authorises object writes per repository, not per branch: on a branch of its
    dataset that is neither `main` nor `run-*` the producer can stage objects but never commit
    them.
  Code: `pkg/auth/declared` (`WithProducedDatasets`), `pkg/kaos/repos` (`repos.go`, `protect.go`,
  `people.go`, `strays.go`), and in the core, each marked: `pkg/catalog/catalog.go`
  (`CreateRepositoryWithMetadata`, `IsBranchBlocked`, `DiscardUncommitted`, `metadataStore`),
  `pkg/graveler/graveler.go` (`CreateRepositoryWithMetadata`, `IsBranchBlocked`,
  `DiscardUncommitted`, `MetadataRepositoryCreator`), `pkg/graveler/ref/manager.go`
  (`CreateRepositoryWithMetadata`).
