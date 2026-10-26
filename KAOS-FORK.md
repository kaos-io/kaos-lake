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
