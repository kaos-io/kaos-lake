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
- **Multi-user auth mode** (PRD novelcore/kaos#1217 F-02). `auth.ui_config.rbac: internal` with no
  `auth.api.endpoint` runs the v1.86 key-value auth store (`contrib/auth/acl`) inside the server, in
  lakeFS's own database: users, groups and policies through the standard auth API and UI, evaluated
  by the unchanged core. Code: `pkg/auth/multiuser`. Every other `rbac` value behaves as upstream.
  - Setup creates upstream's RBAC base groups (Admins, SuperUsers, Developers, Viewers) and policies.
  - A server switched from `rbac: none` migrates on its first start: the basic-mode admin keeps its
    name and key pair and joins Admins. Basic-mode records are not changed, so switching back to
    `none` keeps working with the admin key; users created in multi-user mode exist only there.
