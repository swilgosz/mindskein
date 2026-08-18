#!/usr/bin/env bash
# Lists behaviour scenarios that are declared but not yet implemented.
#
# A scenario is a subtest that calls pending("..."), which fails. Scenarios are
# written from the spec before the code exists, so the suite starts red with the
# whole behaviour list visible and goes green only when every one is satisfied.
# A skip would be worse than useless here: it reports green for work not done.
#
# The gate is therefore just `go test ./...` — this script only makes the
# remaining list readable.
set -uo pipefail

cd "$(dirname "$0")/.."

output=$(go test ./... -v 2>&1)
pending=$(printf '%s\n' "$output" | grep 'PENDING:' | sed 's/^[[:space:]]*//' || true)

if [ -z "$pending" ]; then
	echo "No pending scenarios."
	exit 0
fi

echo "Pending scenarios:"
printf '%s\n' "$pending" | sed 's/^/  /'
echo
printf 'total: %s\n' "$(printf '%s\n' "$pending" | wc -l | tr -d ' ')"
