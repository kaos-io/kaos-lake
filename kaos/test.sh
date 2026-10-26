#!/bin/sh
# kaos-lake test gate run by the release workflow before every image build (PRD kaos#1217).
# Unit tests of the packages KAOS changes or depends on for identity: auth, the ACL store,
# the REST API and the S3 gateway. -short skips tests that need external services, so this
# runs in a plain Go container. Extend the list when KAOS changes another package.
set -eu
cd "$(dirname "$0")/.."
sh kaos/check-lineage_test.sh
go test -short ./pkg/auth/... ./contrib/auth/... ./pkg/api/... ./pkg/gateway/...
