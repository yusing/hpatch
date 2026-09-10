#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	printf 'usage: %s TITLE MARKDOWN\n' "${0##*/}" >&2
	exit 2
fi
: "${MEKUGI_BENCH_ISSUE_DIR:?MEKUGI_BENCH_ISSUE_DIR must be set}"

umask 022
mkdir -p -- "$MEKUGI_BENCH_ISSUE_DIR"
staging=$(mktemp -d "$MEKUGI_BENCH_ISSUE_DIR/.staging-XXXXXX")
cleanup() {
	rm -rf -- "$staging"
}
trap cleanup EXIT HUP INT TERM

printf '%s' "$1" >"$staging/title.txt"
printf '%s' "$2" >"$staging/body.md"

destination=$(mktemp -d "$MEKUGI_BENCH_ISSUE_DIR/report-XXXXXX")
rmdir -- "$destination"
mv -- "$staging" "$destination"
trap - EXIT HUP INT TERM
