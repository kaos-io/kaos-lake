#!/bin/sh
# Tests for kaos/check-lineage.sh against a synthetic history (PRD kaos#1217 AC-08).
# Builds a throwaway repo whose root commit is forced to the v1.86.0 id by replacing
# BASE in a copy of the script, then plants each violation and expects a failure.
#
#   sh kaos/check-lineage_test.sh
set -eu

SCRIPT=$(cd "$(dirname "$0")" && pwd)/check-lineage.sh
work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT
failures=0

git_() { git -c user.name=t -c user.email=t@t -c commit.gpgsign=false "$@"; }

# fresh repo: base (upstream v1.86.0 stand-in) -> upstream/master has one later commit
setup() {
  rm -rf "${work}/r" && mkdir "${work}/r" && cd "${work}/r"
  git_ init -q -b kaos
  printf 'Apache\n' >LICENSE && printf 'notice\n' >NOTICE
  printf 'package a\n' >a.go && printf 'package b\n' >b.go
  git_ add . && git_ commit -qm base
  base=$(git rev-parse HEAD)
  git_ checkout -qb upstream
  printf 'BUSL\n' >LICENSE && printf 'package c // upstream after 1.86\n' >c.go
  git_ add . && git_ commit -qm upstream-later
  git update-ref refs/upstream/master HEAD
  git_ checkout -q kaos
  printf '# KAOS\n' >KAOS-FORK.md && git_ add . && git_ commit -qm kaos-file
  sed "s/^BASE=.*/BASE=${base}/" "${SCRIPT}" >"${work}/check.sh"
}

expect() { # expect pass|fail <name> [reason substring a failure must report]
  if sh "${work}/check.sh" >"${work}/out" 2>&1; then got=pass; else got=fail; fi
  if [ "${got}" = "$1" ] && { [ -z "${3:-}" ] || grep -q "$3" "${work}/out"; }; then
    echo "ok   $2"
  else
    echo "FAIL $2 (expected $1${3:+ with \"$3\"}, got ${got})"; cat "${work}/out"; failures=$((failures + 1))
  fi
}

setup; expect pass "clean KAOS-only history"

setup; git_ merge -q --no-edit refs/upstream/master 2>/dev/null || true; expect fail "merged upstream commit after v1.86.0" "upstream commits made after"

setup; git show refs/upstream/master:c.go >c.go; git_ add c.go; git_ commit -qm copy; expect fail "file copied from upstream after v1.86.0" "identical to upstream"

setup; printf 'changed\n' >>LICENSE; git_ commit -qam lic; expect fail "LICENSE modified" "LICENSE or NOTICE"

setup; printf '// tweak\n' >>a.go; git_ commit -qam tweak; expect fail "modified upstream file without notice" "a.go is modified"

setup; printf '// Modified by KAOS: tweak\n' >>a.go; git_ commit -qam tweak; expect pass "modified upstream file with notice"

setup; printf 'package k\n' >kaos.go; git_ add kaos.go; git_ commit -qm new; expect pass "new KAOS file"

setup; git_ checkout -q --orphan other; git_ commit -qm orphan; expect fail "HEAD not descending from v1.86.0" "does not descend"

[ "${failures}" -eq 0 ] && echo "all lineage tests passed" || { echo "${failures} lineage test(s) failed"; exit 1; }
