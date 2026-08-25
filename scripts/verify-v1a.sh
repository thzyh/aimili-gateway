#!/usr/bin/env bash
set -euo pipefail

export GOTOOLCHAIN=local
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_directory="$(mktemp -d "${TMPDIR:-/tmp}/aimili-gateway-verify.XXXXXX")"

cleanup() {
    rm -rf -- "$build_directory"
}
trap cleanup EXIT

run_step() {
    local name="$1"
    shift
    printf '[verify] %s\n' "$name"
    "$@"
}

assert_no_tracked_match() {
    local category="$1"
    local pattern="$2"
    local matches=''
    local status=0
    if matches="$(git grep -IliE -- "$pattern")"; then
        status=0
    else
        status=$?
    fi
    if [[ $status -gt 1 ]]; then
        printf 'secret scan failed for category %s\n' "$category" >&2
        return 1
    fi
    if [[ -n "$matches" ]]; then
        printf '%s detected in tracked files: %s\n' "$category" "$(printf '%s' "$matches" | tr '\n' ',')" >&2
        return 1
    fi
}

cd "$repository_root"

run_step 'Vue tests' npm test --prefix web
run_step 'Vue production build' npm run build --prefix web
run_step 'Go race tests' go test ./... -race -count=1
run_step 'Go vet' go vet ./...
run_step 'Gateway build' go build -o "$build_directory/aimili-gateway" ./cmd/aimili-gateway
run_step 'Admin CLI build' go build -o "$build_directory/aimili-gateway-admin" ./cmd/aimili-gateway-admin
run_step 'Account command syntax' bash -n deploy/bin/aimili-gateway-account
run_step 'Git whitespace check' git diff --check

printf '[verify] Tracked artifact scan\n'
forbidden_files="$(git ls-files | grep -Ei '(^|/)[.]env([.]|$)|[.](db|db-shm|db-wal|credential|credentials)$|(^|/)(node_modules|dist)(/|$)' || true)"
if [[ -n "$forbidden_files" ]]; then
    printf 'generated or credential artifacts are tracked: %s\n' "$(printf '%s' "$forbidden_files" | tr '\n' ',')" >&2
    exit 1
fi

assert_no_tracked_match 'private-key material' '-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----'
assert_no_tracked_match 'complete proxy connection URI' '(vless|vmess|trojan|ss)://[[:alnum:]]'
assert_no_tracked_match 'UUID-shaped literal' '[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[1-5][0-9A-Fa-f]{3}-[89AaBb][0-9A-Fa-f]{3}-[0-9A-Fa-f]{12}'
assert_no_tracked_match 'literal HTTP credential header' '(cookie|set-cookie|authorization):[[:space:]]*[A-Za-z0-9_-]{16,}'

printf '[verify] V1-A verification passed\n'
