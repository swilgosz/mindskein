#!/usr/bin/env bash
# Reports total statement coverage and fails below a threshold.
#
# -coverpkg=./... is the point: without it each package is scored only by its
# own tests, so tests that drive the CLI get no credit for the packages they
# actually exercise.
set -euo pipefail

threshold="${1:-85}"
cd "$(dirname "$0")/.."

# go test prints failures on stdout, so its output is kept for a failing run.
output=$(go test ./... -coverpkg=./... -coverprofile=cover.out 2>&1) || {
	status=$?
	printf '%s\n' "$output"
	exit "$status"
}
go tool cover -func=cover.out | tail -1

total=$(go tool cover -func=cover.out | awk '/^total:/ {gsub(/%/,"",$NF); print $NF}')
awk -v t="$threshold" -v c="$total" 'BEGIN {
	if (c + 0 < t + 0) { printf "coverage %.1f%% is below the %.1f%% threshold\n", c, t; exit 1 }
	printf "coverage %.1f%% meets the %.1f%% threshold\n", c, t
}'
