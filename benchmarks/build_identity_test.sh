#!/usr/bin/env bash
# Freeze real input bytes; stub only Docker build/inspect operations.
# shellcheck source-path=SCRIPTDIR
set -euo pipefail
benchmark_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=runner/preparation.sh
source "$benchmark_root/runner/preparation.sh"
fixture=$(mktemp -d /tmp/mekugi-build-test-XXXXXX)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/source/benchmarks" "$fixture/run"
cp "$benchmark_root/build_inputs.py" "$fixture/source/benchmarks/"
cp "$benchmark_root/Dockerfile.dockerignore" "$fixture/source/benchmarks/"
printf 'uncommitted implementation\n' >"$fixture/source/router.go"
benchmark_root="$fixture/source/benchmarks" run_dir="$fixture/run"
benchmark_image=mutable-tag
fixture_build_status=0
compose_fixture() {
    printf '%s' "$BENCH_BUILD_CONTEXT" >"$fixture/context-path"
    [[ $(cat "$BENCH_BUILD_CONTEXT/router.go") == 'uncommitted implementation' ]]
    return "$fixture_build_status"
}
docker() {
    case $1 in
        image) printf 'sha256:immutable-image\n' ;;
        run)
            [[ $5 == sha256:immutable-image ]]
            printf 'fixture-binary-sha256\n'
            ;;
        *) return 1 ;;
    esac
}
compose=(compose_fixture)
build_benchmark_image
[[ $benchmark_image == sha256:immutable-image && $BENCH_IMAGE == "$benchmark_image" ]]
[[ ! -d $(cat "$fixture/context-path") ]]
jq -e '.image_id == "sha256:immutable-image" and (.build_inputs_sha256 | length) == 64' \
    "$run_dir/build-identity.json" >/dev/null
fixture_build_status=17
if build_benchmark_image; then exit 1; fi
[[ ! -d $(cat "$fixture/context-path") ]]
[[ -s $run_dir/build-inputs.tar ]]
printf 'Frozen inputs, immutable direct/container image identity and failed-build cleanup passed\n'
