#!/bin/sh
# kaos-lake licence lineage check (PRD kaos#1217 CON-04). Exits non-zero when the checked-out
# commit could contain lakeFS upstream code released after v1.86.0, which is BUSL-1.1.
#
#   kaos/check-lineage.sh [upstream-ref]
#
# upstream-ref is a local ref holding upstream lakeFS history after v1.86.0 (default
# refs/upstream/master; the release workflow fetches treeverse/lakeFS master into it).
#
# Checks:
#   1. HEAD descends from the upstream v1.86.0 commit.
#   2. No commit between v1.86.0 and HEAD is an upstream commit (merge, rebase, cherry-pick
#      with the same id).
#   3. No file in HEAD is byte-identical to a file version upstream introduced after v1.86.0
#      (catches copies that bypass git history).
#   4. LICENSE and NOTICE are unchanged from v1.86.0.
#   5. Every upstream file KAOS modified carries the notice "Modified by KAOS"
#      (Apache-2.0 section 4(b)); KAOS-only files and go.sum (generated) are exempt.
set -eu

BASE=36fad6476e23c118b91247523a0c7fb045cead4e
UPSTREAM=${1:-refs/upstream/master}
NOTICE_MARK="Modified by KAOS"
fail=0

err() { echo "FAIL: $*" >&2; fail=1; }

git rev-parse --verify --quiet "${BASE}^{commit}" >/dev/null || { echo "FAIL: v1.86.0 commit ${BASE} not in history" >&2; exit 1; }
git rev-parse --verify --quiet "${UPSTREAM}^{commit}" >/dev/null || { echo "FAIL: upstream ref ${UPSTREAM} missing (fetch it first)" >&2; exit 1; }

# 1. lineage
git merge-base --is-ancestor "${BASE}" HEAD || err "HEAD does not descend from lakeFS v1.86.0 (${BASE})"

tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

# 2. upstream commits after v1.86.0 reachable from HEAD
git rev-list "${BASE}..HEAD" | sort >"${tmp}/ours"
git rev-list "${BASE}..${UPSTREAM}" | sort >"${tmp}/upstream"
comm -12 "${tmp}/ours" "${tmp}/upstream" >"${tmp}/shared"
if [ -s "${tmp}/shared" ]; then
  err "HEAD contains upstream commits made after v1.86.0:"
  sed 's/^/  /' "${tmp}/shared" >&2
fi

# 3. file contents introduced upstream after v1.86.0
git ls-tree -r "${BASE}" | awk '$2 == "blob" {print $3}' | sort -u >"${tmp}/base-blobs"
git ls-tree -r HEAD | awk '$2 == "blob" {print $3 " " $4}' | sort >"${tmp}/head-blobs"
git rev-list --objects "${BASE}..${UPSTREAM}" | awk 'NF == 2 {print $1}' | sort -u >"${tmp}/upstream-objs"
comm -23 "${tmp}/upstream-objs" "${tmp}/base-blobs" >"${tmp}/new-upstream-blobs"
awk 'NR == FNR {new[$1] = 1; next} ($1 in new) {print $2}' "${tmp}/new-upstream-blobs" "${tmp}/head-blobs" >"${tmp}/copied"
if [ -s "${tmp}/copied" ]; then
  err "files identical to upstream versions released after v1.86.0:"
  sed 's/^/  /' "${tmp}/copied" >&2
fi

# 4. licence files
git diff --quiet "${BASE}" HEAD -- LICENSE NOTICE || err "LICENSE or NOTICE differ from lakeFS v1.86.0"

# 5. modification notices on changed upstream files. go.sum is exempt: generated checksums,
#    which cannot carry a comment; the go.mod beside it records the KAOS change.
git diff --name-only --diff-filter=M "${BASE}" HEAD >"${tmp}/modified"
while IFS= read -r f; do
  [ -n "${f}" ] || continue
  [ "$(basename "${f}")" = go.sum ] && continue
  grep -q "${NOTICE_MARK}" "${f}" || err "${f} is modified from upstream but lacks \"${NOTICE_MARK}\""
done <"${tmp}/modified"

if [ "${fail}" -ne 0 ]; then
  echo "lineage check FAILED" >&2
  exit 1
fi
echo "lineage check passed: $(git rev-parse --short HEAD) descends only from lakeFS v1.86.0 plus KAOS changes"
