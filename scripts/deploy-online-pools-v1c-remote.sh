#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

die() {
    printf 'V1-C 部署失败：%s\n' "$*" >&2
    exit 1
}

validate_capacity() {
    [[ "$1" =~ ^[0-9]+$ && "$1" -ge 1 && "$1" -le 64 ]] || die "容量必须在 1-64 之间"
}

wait_for_service() {
    local service="$1" port="$2" attempt
    for attempt in $(seq 1 60); do
        if systemctl is-active --quiet "$service" && ss -lntH | grep -q "127.0.0.1:${port}"; then
            return 0
        fi
        sleep 2
    done
    die "$service 未在预期时间内恢复"
}

set_gateway_capacity() {
    local capacity="$1"
    validate_capacity "$capacity"
    python3 - "$capacity" <<'PY'
import json
import os
import sys

capacity = int(sys.argv[1])
path = "/etc/aimili-gateway/config.json"
with open(path, encoding="utf-8") as handle:
    d = json.load(handle)
d["maxProxyGroups"] = capacity
with open(path, "w", encoding="utf-8") as handle:
    json.dump(d, handle, ensure_ascii=False, indent=2)
os.chmod(path, 0o600)
PY
}

restart_gateway() {
    systemctl restart aimili-gateway.service
    wait_for_service aimili-gateway.service 9080
    curl -fsS http://127.0.0.1:9080/healthz >/dev/null
}

if [[ $# -eq 2 && "$1" == "capacity" ]]; then
    set_gateway_capacity "$2"
    restart_gateway
    printf 'gateway_capacity=%s\n' "$2"
    exit 0
fi

[[ $# -eq 6 && "$1" == "install" ]] || die "用法：install <gateway_sha> <admin_sha> <bundle_sha> <aimili_commit> <capacity>，或 capacity <数量>"
gateway_sha="$2"
admin_sha="$3"
bundle_sha="$4"
aimili_commit="$5"
capacity="$6"
validate_capacity "$capacity"
for value in "$gateway_sha" "$admin_sha" "$bundle_sha" "$aimili_commit"; do
    [[ "$value" =~ ^[0-9a-f]{40,64}$ ]] || die "提交或哈希格式无效"
done

find /var/backups/aimili-v1c -type f -name pre-v1c.tar.gz -size +0c -print -quit | grep -q . \
    || die "未找到有效的部署前回滚包"

check_hash() {
    local expected="$1" path="$2" actual
    [[ -f "$path" ]] || die "缺少暂存文件 $path"
    actual="$(sha256sum "$path" | awk '{print $1}')"
    [[ "$actual" == "$expected" ]] || die "暂存文件哈希不匹配：$path"
}
check_hash "$gateway_sha" /tmp/aimili-gateway
check_hash "$admin_sha" /tmp/aimili-gateway-admin
check_hash "$bundle_sha" /tmp/aimili-vpngate-v1c.bundle

git -C /opt/aimilivpn bundle verify /tmp/aimili-vpngate-v1c.bundle >/dev/null
if [[ -n "$(git -C /opt/aimilivpn status --porcelain)" ]]; then
    git -C /opt/aimilivpn stash push --include-untracked -m pre-v1c-deploy >/dev/null
fi
git -C /opt/aimilivpn fetch /tmp/aimili-vpngate-v1c.bundle feature/v1c-online-pools
[[ "$(git -C /opt/aimilivpn rev-parse FETCH_HEAD)" == "$aimili_commit" ]] || die "AimiliVPN bundle 提交不匹配"
git -C /opt/aimilivpn merge --ff-only FETCH_HEAD

install -d -m 0755 /etc/systemd/system/aimilivpn.service.d
printf '%s\n' \
    '[Unit]' \
    'StartLimitIntervalSec=600' \
    'StartLimitBurst=3' \
    '' \
    '[Service]' \
    'Environment=OPENVPN_TEST_CONCURRENCY=1' \
    'Environment=TCP_PRESCREEN_CONCURRENCY=8' \
    'Environment=MAX_FETCH_ROWS=300' \
    'Environment=TARGET_VALID_POOL_SIZE=30' \
    'Environment=NODE_TEST_BATCH_SIZE=1' \
    'Environment=PROBE_FAILURE_COOLDOWN_SECONDS=1800' \
    'Environment=MAX_EXIT_SLOTS=4' \
    'Environment=COLLECTOR_INITIAL_DELAY_SECONDS=120' \
    'Environment=COLLECTOR_FAILURE_BACKOFF_SECONDS=600' \
    'Environment=FETCH_INTERVAL_SECONDS=21600' \
    'Environment=CHECK_INTERVAL_SECONDS=21600' \
    'Environment=LOCAL_PROXY_MAX_CONNECTIONS=128' \
    'Environment=LOCAL_PROXY_MAX_CONNECTIONS_PER_LISTENER=64' \
    'Environment=AIMILI_CONTROL_ADDRESS=127.0.0.1:8790' \
    'Environment=MALLOC_ARENA_MAX=2' \
    'Restart=on-failure' \
    'RestartSec=30' \
    'MemoryHigh=180M' \
    'MemoryMax=220M' \
    'TasksMax=160' \
    'CPUQuota=50%' \
    'Nice=10' \
    'OOMScoreAdjust=500' \
    > /etc/systemd/system/aimilivpn.service.d/20-v1c-capacity.conf
chmod 0644 /etc/systemd/system/aimilivpn.service.d/20-v1c-capacity.conf
systemctl daemon-reload
systemctl restart aimilivpn.service
wait_for_service aimilivpn.service 8790

python3 - <<'PY'
import json
import urllib.request

token = open("/etc/aimilivpn/control.token", encoding="utf-8").read().strip()
headers = {"Authorization": "Bearer " + token, "Accept": "application/json"}
for path in ("/control/v1/capabilities", "/control/v1/slots"):
    request = urllib.request.Request("http://127.0.0.1:8790" + path, headers=headers)
    with urllib.request.urlopen(request, timeout=10) as response:
        json.load(response)
PY

cp -a --no-clobber /usr/local/bin/aimili-gateway /usr/local/bin/aimili-gateway.pre-v1c
cp -a --no-clobber /usr/local/bin/aimili-gateway-admin /usr/local/bin/aimili-gateway-admin.pre-v1c
install -m 0755 /tmp/aimili-gateway /usr/local/bin/aimili-gateway.v1c
install -m 0755 /tmp/aimili-gateway-admin /usr/local/bin/aimili-gateway-admin.v1c
set_gateway_capacity "$capacity"
systemctl stop aimili-gateway.service
mv /usr/local/bin/aimili-gateway.v1c /usr/local/bin/aimili-gateway
mv /usr/local/bin/aimili-gateway-admin.v1c /usr/local/bin/aimili-gateway-admin
if ! systemctl start aimili-gateway.service; then
    cp -a /usr/local/bin/aimili-gateway.pre-v1c /usr/local/bin/aimili-gateway
    cp -a /usr/local/bin/aimili-gateway-admin.pre-v1c /usr/local/bin/aimili-gateway-admin
    systemctl start aimili-gateway.service || true
    die "Gateway 新二进制启动失败，已尝试恢复旧二进制"
fi
wait_for_service aimili-gateway.service 9080
curl -fsS http://127.0.0.1:9080/healthz >/dev/null

python3 - <<'PY'
import sqlite3

database = sqlite3.connect("/var/lib/aimili-gateway/aimili-gateway.db")
versions = {row[0] for row in database.execute("select version from schema_migrations")}
if 5 not in versions:
    raise SystemExit("schema 5 migration is missing")
database.close()
PY

printf 'v1c_install=true\n'
printf 'gateway_capacity=%s\n' "$capacity"
