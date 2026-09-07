#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
stage="$repo_root/deploy/local-vm/native/stage.sh"
backup="$repo_root/deploy/local-vm/native/backup.sh"
rollback="$repo_root/deploy/local-vm/native/rollback.sh"
fixture="$(mktemp -d)"
trap 'rm -rf -- "$fixture"' EXIT

mkdir -p "$fixture/assets/bin" "$fixture/targets/etc/aimili-gateway" "$fixture/targets/var/lib/aimili-gateway"
printf 'gateway-v1\n' > "$fixture/assets/bin/aimili-gateway"
printf 'old-config\n' > "$fixture/targets/etc/aimili-gateway/config.json"
printf 'old-state\n' > "$fixture/targets/var/lib/aimili-gateway/state"
sha256="$(sha256sum "$fixture/assets/bin/aimili-gateway" | awk '{print $1}')"
cat > "$fixture/assets/manifest.json" <<JSON
{"schemaVersion":1,"files":[{"path":"bin/aimili-gateway","sha256":"$sha256"}]}
JSON

if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/manifest.json" run-1 >/dev/null 2>&1; then
  test -s "$fixture/staging/run-1/bin/aimili-gateway"
else
  echo 'stage valid manifest rejected' >&2
  exit 1
fi
test ! -e "$fixture/staging/run-1.tmp"

cp "$fixture/assets/manifest.json" "$fixture/assets/bad.json"
sed -i 's/"sha256":"[0-9a-f]*/"sha256":"0000000000000000000000000000000000000000000000000000000000000000/' "$fixture/assets/bad.json"
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/bad.json" run-bad >/dev/null 2>&1; then
  echo 'digest mismatch accepted' >&2
  exit 1
fi
test ! -e "$fixture/staging/run-bad"

cat > "$fixture/assets/escape.json" <<'JSON'
{"schemaVersion":1,"files":[{"path":"../escape","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}
JSON
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/escape.json" run-escape >/dev/null 2>&1; then
  echo 'path escape accepted' >&2
  exit 1
fi

cat > "$fixture/assets/unknown.json" <<JSON
{"schemaVersion":1,"files":[{"path":"bin/aimili-gateway","sha256":"$sha256"}],"unknown":true}
JSON
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/unknown.json" run-unknown >/dev/null 2>&1; then
  echo 'unknown manifest field accepted' >&2
  exit 1
fi

ln -s bin/aimili-gateway "$fixture/assets/link"
cat > "$fixture/assets/link.json" <<JSON
{"schemaVersion":1,"files":[{"path":"link","sha256":"$sha256"}]}
JSON
if env AIMILI_STAGING_ROOT="$fixture/staging" bash "$stage" "$fixture/assets/link.json" run-link >/dev/null 2>&1; then
  echo 'symlink asset accepted' >&2
  exit 1
fi

printf '#!/bin/sh\nprintf daemon-reload >> "$SYSTEMCTL_LOG"\n' > "$fixture/systemctl"
chmod 0700 "$fixture/systemctl"
printf 'before\n' > "$fixture/targets/etc/aimili-gateway/config.json"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$backup" --component gateway --run-id run-1 --backup-root "$fixture/backups"
printf 'after\n' > "$fixture/targets/etc/aimili-gateway/config.json"
rm -f "$fixture/targets/var/lib/aimili-gateway/state"
mkdir -p "$fixture/targets/etc/systemd/system"
printf 'new-unit\n' > "$fixture/targets/etc/systemd/system/aimili-gateway.service"
env AIMILI_NATIVE_ROOT="$fixture/targets" PATH="$fixture:$PATH" SYSTEMCTL_LOG="$fixture/systemctl.log" bash "$rollback" --component gateway --run-id run-1 --backup-root "$fixture/backups"
grep -qx 'before' "$fixture/targets/etc/aimili-gateway/config.json"
test -s "$fixture/targets/var/lib/aimili-gateway/state"
test ! -e "$fixture/targets/etc/systemd/system/aimili-gateway.service"
grep -qx daemon-reload "$fixture/systemctl.log"

if env AIMILI_NATIVE_ROOT="$fixture/targets" bash "$rollback" --component gateway --run-id run-1 --backup-root "$fixture/backups" --target-dir /etc/passwd >/dev/null 2>&1; then
  echo 'caller supplied arbitrary rollback target accepted' >&2
  exit 1
fi

echo 'PASS native orchestration fixture'
