#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || { printf '%s\n' '用法：deploy-external-ui-stage-a-remote.sh <staging绝对路径>' >&2; exit 2; }
readonly INPUT_STAGE="$1"
readonly STAGE="$(realpath -e -- "$INPUT_STAGE")"
[[ "$STAGE" =~ ^/tmp/aimili-gateway-ui-stage-a-[0-9]{8,14}$ ]] || { printf '%s\n' 'staging路径不符合固定范围。' >&2; exit 2; }
readonly UI_ROOT=/var/lib/aimili-gateway/ui
readonly BACKUP=/var/backups/aimili-gateway/20260905-external-ui
readonly CONFIG=/etc/aimili-gateway/config.json
readonly UNIT=/etc/systemd/system/aimili-gateway.service
readonly GATEWAY=/usr/local/bin/aimili-gateway
readonly INSTALLER=/usr/local/bin/aimili-gateway-update-install
readonly PUBLIC_KEY=/etc/aimili-gateway/ui-release.pub
readonly DB=/var/lib/aimili-gateway/aimili-gateway.db

for asset in aimili-gateway aimili-gateway-update-install aimili-gateway.service ui-release.pub SHA256SUMS; do
    [[ -f "$STAGE/$asset" && ! -L "$STAGE/$asset" ]] || { printf '缺少普通文件：%s\n' "$asset" >&2; exit 1; }
done
for asset in manifest.json manifest.sig ui.tar.gz; do
    [[ -f "$STAGE/ui/$asset" && ! -L "$STAGE/ui/$asset" ]] || { printf '缺少普通文件：ui/%s\n' "$asset" >&2; exit 1; }
done
(cd "$STAGE" && sha256sum -c SHA256SUMS)
[[ ! -e "$BACKUP" ]] || { printf '%s\n' '本轮唯一备份目录已经存在，拒绝覆盖。' >&2; exit 1; }
systemctl is-active --quiet aimili-gateway.service aimilivpn.service x-ui.service caddy.service

readonly BEFORE_GATEWAY_PID="$(systemctl show aimili-gateway.service -p MainPID --value)"
readonly BEFORE_AIMILI_PID="$(systemctl show aimilivpn.service -p MainPID --value)"
readonly BEFORE_XUI_PID="$(systemctl show x-ui.service -p MainPID --value)"
readonly BEFORE_CADDY_PID="$(systemctl show caddy.service -p MainPID --value)"
readonly BEFORE_OPENVPN="$(pgrep -xc openvpn || true)"
readonly BEFORE_XRAY="$(pgrep -fc 'xray-linux-amd64' || true)"

python3 - "$DB" <<'PY'
import sqlite3, sys
with sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True) as db:
    assert db.execute('PRAGMA quick_check').fetchone()[0] == 'ok'
    assert db.execute('SELECT COUNT(*) FROM egress_protocol_modes WHERE state = ?', ('ready',)).fetchone()[0] == 4
PY

install -d -m 0700 -o root -g root "$BACKUP"
install -m 0755 "$GATEWAY" "$BACKUP/aimili-gateway"
install -m 0644 "$UNIT" "$BACKUP/aimili-gateway.service"
install -m 0600 "$CONFIG" "$BACKUP/config.json"

rollback() {
    local status=$?
    trap - ERR
    install -m 0755 "$BACKUP/aimili-gateway" "$GATEWAY"
    install -m 0644 "$BACKUP/aimili-gateway.service" "$UNIT"
    install -m 0600 "$BACKUP/config.json" "$CONFIG"
    systemctl daemon-reload
    systemctl restart aimili-gateway.service
    systemctl is-active --quiet aimili-gateway.service
    printf 'Stage A失败，Gateway已恢复；状态=%s\n' "$status" >&2
    exit "$status"
}
trap rollback ERR

install -d -m 0755 -o root -g root "$UI_ROOT" "$UI_ROOT/releases"
install -m 0755 "$STAGE/aimili-gateway-update-install" "$INSTALLER"
install -m 0644 "$STAGE/ui-release.pub" "$PUBLIC_KEY"

python3 - "$CONFIG" <<'PY'
import json, os, pathlib, stat, sys, tempfile
path = pathlib.Path(sys.argv[1])
info = path.stat()
data = json.loads(path.read_text(encoding='utf-8'))
data['externalUiRoot'] = '/var/lib/aimili-gateway/ui'
fd, temporary = tempfile.mkstemp(prefix='.config-ui-', dir=path.parent)
try:
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as out:
        json.dump(data, out, ensure_ascii=False, separators=(',', ':'))
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())
    os.chown(temporary, info.st_uid, info.st_gid)
    os.chmod(temporary, stat.S_IMODE(info.st_mode))
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
PY

install -m 0755 "$STAGE/aimili-gateway" "$GATEWAY"
install -m 0644 "$STAGE/aimili-gateway.service" "$UNIT"
systemctl daemon-reload
systemctl restart aimili-gateway.service
for _ in $(seq 1 20); do
    if curl --fail --silent --show-error --max-time 3 http://127.0.0.1:9080/healthz >/dev/null; then break; fi
    sleep 1
done
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:9080/healthz >/dev/null
"$INSTALLER" ui-install --root "$UI_ROOT" --staging "$STAGE/ui" --public-key "$PUBLIC_KEY" --health-url http://127.0.0.1:9080
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:9080/manifest.json >/dev/null
systemctl is-active --quiet aimili-gateway.service aimilivpn.service x-ui.service caddy.service
[[ "$(systemctl show aimilivpn.service -p MainPID --value)" == "$BEFORE_AIMILI_PID" ]]
[[ "$(systemctl show x-ui.service -p MainPID --value)" == "$BEFORE_XUI_PID" ]]
[[ "$(systemctl show caddy.service -p MainPID --value)" == "$BEFORE_CADDY_PID" ]]
[[ "$(pgrep -xc openvpn || true)" == "$BEFORE_OPENVPN" ]]
[[ "$(pgrep -fc 'xray-linux-amd64' || true)" == "$BEFORE_XRAY" ]]
[[ "$(systemctl show aimili-gateway.service -p MainPID --value)" != "$BEFORE_GATEWAY_PID" ]]
python3 - "$DB" <<'PY'
import sqlite3, sys
with sqlite3.connect('file:' + sys.argv[1] + '?mode=ro', uri=True) as db:
    assert db.execute('PRAGMA quick_check').fetchone()[0] == 'ok'
    assert db.execute('SELECT COUNT(*) FROM egress_protocol_modes WHERE state = ?', ('ready',)).fetchone()[0] == 4
PY

trap - ERR
printf '%s\n' 'PASS external-ui-stage-a'
