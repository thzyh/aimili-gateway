#!/usr/bin/env bash
set -euo pipefail

[[ "${EUID}" -eq 0 ]] || { printf '%s\n' '必须以 root 运行。' >&2; exit 1; }
[[ $# -eq 1 ]] || { printf '%s\n' '用法：deploy-main-switch-protocol-modes-remote.sh <1|2|3|4|5|6>' >&2; exit 2; }

readonly CURRENT_STAGE="$1"
readonly ASSET_ROOT=/tmp/aimili-main-switch-protocol-modes
readonly VERIFY="$ASSET_ROOT/verify-main-switch-protocol-modes-remote.py"
readonly STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
readonly BACKUP_ROOT="/var/backups/aimili-gateway/main-switch-protocol-modes/${STAMP}"
readonly GATEWAY_DB=/var/lib/aimili-gateway/aimili-gateway.db
readonly XUI_DB=/etc/x-ui/x-ui.db
readonly XRAY_RUNTIME=/usr/local/x-ui/bin/config.json
readonly AIMILI_REPOSITORY="$(systemctl show aimilivpn.service --property=WorkingDirectory --value)"
case "$CURRENT_STAGE" in 1|2|3|4|5|6) ;; *) printf '%s\n' '阶段必须为 1 到 6。' >&2; exit 2 ;; esac
[[ -x /usr/local/x-ui/bin/xray-linux-amd64 && -s "$VERIFY" ]] || { printf '%s\n' '部署资产不完整。' >&2; exit 1; }
[[ "$AIMILI_REPOSITORY" == /* && ! -L "$AIMILI_REPOSITORY" && -d "$AIMILI_REPOSITORY/.git" && -f "$AIMILI_REPOSITORY/vpngate_manager.py" ]] || { printf '%s\n' 'AimiliVPN 工作目录无效。' >&2; exit 1; }
[[ -z "$(git -C "$AIMILI_REPOSITORY" status --porcelain)" ]] || { printf '%s\n' 'AimiliVPN 工作区不干净。' >&2; exit 1; }
cd "$ASSET_ROOT"
sha256sum -c "$ASSET_ROOT/SHA256SUMS" >/dev/null
readonly TARGET_AIMILI_COMMIT="$(<"$ASSET_ROOT/aimili-target-commit")"
[[ "$TARGET_AIMILI_COMMIT" =~ ^[0-9a-f]{40}$ ]] || { printf '%s\n' 'AimiliVPN 目标提交无效。' >&2; exit 1; }

resource_gate() {
    local memory swap
    memory="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)"
    swap="$(awk '/^SwapFree:/ {print $2}' /proc/meminfo)"
    [[ "$memory" -ge 163840 ]] || { printf '%s\n' 'resource_gate=memory_below_threshold' >&2; return 1; }
    [[ "$swap" -ge 524288 ]] || { printf '%s\n' 'resource_gate=swap_below_threshold' >&2; return 1; }
    printf 'MemAvailable=%sMiB SwapFree=%sMiB\n' "$((memory / 1024))" "$((swap / 1024))"
}

server_xray_pid() {
    local expected process resolved matches=()
    expected="$(readlink -f /usr/local/x-ui/bin/xray-linux-amd64)"
    for process in /proc/[0-9]*; do
        resolved="$(readlink -f "$process/exe" 2>/dev/null || true)"
        if [[ "$resolved" == "$expected" ]]; then
            matches+=("${process##*/}")
        fi
    done
    [[ "${#matches[@]}" -eq 1 ]] || return 1
    printf '%s\n' "${matches[0]}"
}

create_joint_backup() {
    install -d -m 0700 "$BACKUP_ROOT"
    cp -a /usr/local/bin/aimili-gateway "$BACKUP_ROOT/aimili-gateway"
    cp -a /etc/aimili-gateway/config.json "$BACKUP_ROOT/config.json"
    cp -a "$XRAY_RUNTIME" "$BACKUP_ROOT/xray-runtime.json"
    cp -a /etc/caddy/Caddyfile "$BACKUP_ROOT/Caddyfile"
    cp -a "$AIMILI_REPOSITORY/vpngate_data" "$BACKUP_ROOT/aimilivpn-state"
    cp -a /etc/aimilivpn "$BACKUP_ROOT/aimilivpn-control"
    git -C "$AIMILI_REPOSITORY" bundle create "$BACKUP_ROOT/aimilivpn.bundle" HEAD
    git -C "$AIMILI_REPOSITORY" rev-parse HEAD > "$BACKUP_ROOT/aimilivpn-head"
    python3 - "$BACKUP_ROOT" "$GATEWAY_DB" "$XUI_DB" <<'PY'
import pathlib, sqlite3, sys
target = pathlib.Path(sys.argv[1])
for source, name in ((sys.argv[2], "aimili-gateway.db"), (sys.argv[3], "x-ui.db")):
    original = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    backup = sqlite3.connect(target / name)
    original.backup(backup)
    backup.close(); original.close()
PY
    ufw status numbered > "$BACKUP_ROOT/ufw-status.txt"
    python3 "$VERIFY" fingerprint --xui-db "$XUI_DB" > "$BACKUP_ROOT/unmanaged-resources.json"
    server_xray_pid > "$BACKUP_ROOT/xray.pid"
    printf '%s\n' '{"status":"not_started"}' > "$BACKUP_ROOT/non-target-probes.json"
    : > "$BACKUP_ROOT/ufw-added.txt"
    for item in \
        /usr/local/bin/aimili-xui-protocol-transaction \
        /usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py \
        /etc/aimili-gateway/protocol-transaction.json \
        /etc/systemd/system/aimili-gateway.service \
        /etc/systemd/system/aimili-xui-protocol-transaction.path \
        /etc/systemd/system/aimili-xui-protocol-transaction.service \
        /etc/systemd/system/aimili-xui-protocol-transaction.timer
    do
        name="$(printf '%s' "$item" | sha256sum | awk '{print $1}')"
        if [[ -e "$item" ]]; then cp -a "$item" "$BACKUP_ROOT/optional-$name"; else : > "$BACKUP_ROOT/optional-$name.absent"; fi
    done
    find "$BACKUP_ROOT" -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > "$BACKUP_ROOT/SHA256SUMS"
    chmod -R go-rwx "$BACKUP_ROOT"
}

restore_optional_asset() {
    local target name
    target="$1"
    name="$(printf '%s' "$target" | sha256sum | awk '{print $1}')"
    if [[ -f "$BACKUP_ROOT/optional-$name.absent" ]]; then
        rm -f -- "$target"
    else
        cp -a "$BACKUP_ROOT/optional-$name" "$target"
    fi
}

rollback_added_udp_rules() {
    while IFS= read -r rule; do
        [[ -n "$rule" ]] && ufw --force delete allow "$rule" >/dev/null 2>&1 || true
    done < "$BACKUP_ROOT/ufw-added.txt"
}

ensure_udp_rule() {
    local rule="$1"
    if ! ufw status | awk -v rule="$rule" '$1 == rule && $2 == "ALLOW" { found=1 } END { exit(found ? 0 : 1) }'; then
        ufw allow "$rule"
        printf '%s\n' "$rule" >> "$BACKUP_ROOT/ufw-added.txt"
    fi
}

verify_aimili_stage() {
    local ready=false
    for _ in $(seq 1 60); do
        if systemctl is-active --quiet aimilivpn.service \
            && ss -lntH | grep -qE '127\.0\.0\.1:7928\b' \
            && ss -lntH | grep -qE '127\.0\.0\.1:8790\b'; then
            ready=true
            break
        fi
        sleep 2
    done
    [[ "$ready" == true ]]
    python3 - <<'PY'
import json, pathlib, urllib.request
token = pathlib.Path('/etc/aimilivpn/control.token').read_text(encoding='utf-8').strip()
request = urllib.request.Request(
    'http://127.0.0.1:8790/control/v1/capabilities',
    headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/json'},
)
with urllib.request.urlopen(request, timeout=15) as response:
    document = json.load(response)
capabilities = set(document.get('data', {}).get('capabilities', []))
required = {'main.assign', 'main.assign.commit', 'main.assign.rollback', 'main.assignment.read'}
if not required.issubset(capabilities):
    raise SystemExit('main_assignment_capability_missing')
PY
}

protocol_stage2_postcheck() {
    local ready=false current_xray unmanaged_after
    for _ in $(seq 1 45); do
        if systemctl is-active --quiet aimili-gateway.service \
            && curl --silent --show-error --fail --max-time 5 http://127.0.0.1:9080/healthz >/dev/null; then
            ready=true
            break
        fi
        sleep 2
    done
    [[ "$ready" == true ]]
    python3 - "$GATEWAY_DB" "$XUI_DB" <<'PY'
import sqlite3, sys
gateway = sqlite3.connect(f'file:{sys.argv[1]}?mode=ro', uri=True)
assert gateway.execute('SELECT COUNT(*) FROM schema_migrations WHERE version=10').fetchone()[0] == 1
assert gateway.execute('SELECT COUNT(*) FROM egress_protocol_modes').fetchone()[0] == 4
gateway.close()
xui = sqlite3.connect(f'file:{sys.argv[2]}?mode=ro', uri=True)
rows = xui.execute('SELECT tag,port FROM inbounds').fetchall(); xui.close()
managed = [row for row in rows if row[0] == 'aimili-reality' or str(row[0]).startswith('agw-')]
assert sum(row[1] in (8443,20000,20001,20002) for row in managed) == 4
assert sum(row[1] in (30000,30001,30002,31000) for row in managed) == 4
PY
    current_xray="$(server_xray_pid)"
    [[ -n "$current_xray" && "$current_xray" == "$(<"$BACKUP_ROOT/xray.pid")" ]]
    unmanaged_after="$(python3 "$VERIFY" fingerprint --xui-db "$XUI_DB")"
    [[ "$unmanaged_after" == "$(<"$BACKUP_ROOT/unmanaged-resources.json")" ]]
    python3 "$VERIFY" preflight --xui-db "$XUI_DB" --require-udp >/dev/null
}

rollback_current_stage() {
    set +e
    case "$CURRENT_STAGE" in
        1)
            old_head="$(<"$BACKUP_ROOT/aimilivpn-head")"
            git -C "$AIMILI_REPOSITORY" reset --keep "$old_head"
            cp -a "$BACKUP_ROOT/aimilivpn-state/." "$AIMILI_REPOSITORY/vpngate_data/"
            cp -a "$BACKUP_ROOT/aimilivpn-control/." /etc/aimilivpn/
            systemctl try-restart aimilivpn.service
            ;;
        2)
            systemctl stop aimili-gateway.service
            cp -a "$BACKUP_ROOT/aimili-gateway" /usr/local/bin/aimili-gateway
            chown root:root /usr/local/bin/aimili-gateway
            chmod 0755 /usr/local/bin/aimili-gateway
            cp -a "$BACKUP_ROOT/config.json" /etc/aimili-gateway/config.json
            chown aimili-gateway:aimili-gateway /etc/aimili-gateway/config.json
            chmod 0600 /etc/aimili-gateway/config.json
            cp -a "$BACKUP_ROOT/aimili-gateway.db" "$GATEWAY_DB"
            chown aimili-gateway:aimili-gateway "$GATEWAY_DB"
            chmod 0600 "$GATEWAY_DB"
            restore_optional_asset /usr/local/bin/aimili-xui-protocol-transaction
            restore_optional_asset /usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py
            restore_optional_asset /etc/aimili-gateway/protocol-transaction.json
            restore_optional_asset /etc/systemd/system/aimili-gateway.service
            restore_optional_asset /etc/systemd/system/aimili-xui-protocol-transaction.path
            restore_optional_asset /etc/systemd/system/aimili-xui-protocol-transaction.service
            restore_optional_asset /etc/systemd/system/aimili-xui-protocol-transaction.timer
            rollback_added_udp_rules
            systemctl daemon-reload
            systemctl start aimili-gateway.service
            ;;
        3|4|5|6)
            /usr/local/bin/aimili-xui-protocol-transaction --config /etc/aimili-gateway/protocol-transaction.json recover >/dev/null
            ;;
    esac
    printf 'stage=%s rollback=attempted\n' "$CURRENT_STAGE" >&2
}
trap 'rollback_current_stage; exit 1' ERR

resource_gate
python3 "$VERIFY" preflight --xui-db "$XUI_DB" --protocol-config "$ASSET_ROOT/protocol-transaction.json"
create_joint_backup

case "$CURRENT_STAGE" in
    1)
        git -C "$AIMILI_REPOSITORY" bundle verify "$ASSET_ROOT/aimili-vpngate.bundle" >/dev/null
        git -C "$AIMILI_REPOSITORY" fetch "$ASSET_ROOT/aimili-vpngate.bundle" refs/heads/feat/main-switch-protocol-modes
        [[ "$(git -C "$AIMILI_REPOSITORY" rev-parse FETCH_HEAD)" == "$TARGET_AIMILI_COMMIT" ]]
        git -C "$AIMILI_REPOSITORY" merge --ff-only FETCH_HEAD
        systemctl try-restart aimilivpn.service
        verify_aimili_stage
        ;;
    2)
        install -d -m 0750 -o aimili-gateway -g aimili-gateway /var/lib/aimili-gateway/protocol-spool
        install -d -m 0700 -o aimili-gateway -g aimili-gateway /var/lib/aimili-gateway/protocol-spool/requests
        install -d -m 0750 -o root -g aimili-gateway /var/lib/aimili-gateway/protocol-spool/results
        install -d -m 0700 -o root -g root /var/lib/aimili-xui-protocol-transaction/transactions
        install -d -m 0700 -o root -g root /var/lib/aimili-xui-protocol-transaction/profiles
        install -d -m 0755 -o root -g root /usr/lib/aimili-gateway
        install -o root -g root -m 0755 "$ASSET_ROOT/aimili-gateway" /usr/local/bin/aimili-gateway
        install -o root -g root -m 0755 "$ASSET_ROOT/aimili-xui-protocol-transaction" /usr/local/bin/aimili-xui-protocol-transaction
        install -o root -g root -m 0644 "$ASSET_ROOT/aimili_xui_protocol_transaction.py" /usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py
        install -o root -g root -m 0644 "$ASSET_ROOT/aimili-gateway.service" /etc/systemd/system/aimili-gateway.service
        install -o root -g root -m 0644 "$ASSET_ROOT/aimili-xui-protocol-transaction.path" /etc/systemd/system/aimili-xui-protocol-transaction.path
        install -o root -g root -m 0644 "$ASSET_ROOT/aimili-xui-protocol-transaction.service" /etc/systemd/system/aimili-xui-protocol-transaction.service
        install -o root -g root -m 0644 "$ASSET_ROOT/aimili-xui-protocol-transaction.timer" /etc/systemd/system/aimili-xui-protocol-transaction.timer
        install -o root -g root -m 0640 "$ASSET_ROOT/protocol-transaction.json" /etc/aimili-gateway/protocol-transaction.json
        python3 - /etc/aimili-gateway/config.json <<'PY'
import json, os, pathlib, tempfile, sys
path = pathlib.Path(sys.argv[1])
config = json.loads(path.read_text(encoding="utf-8"))
config["protocolRequestDir"] = "/var/lib/aimili-gateway/protocol-spool/requests"
config["protocolResultDir"] = "/var/lib/aimili-gateway/protocol-spool/results"
config["protocolTimeoutSeconds"] = 180
stat = path.stat()
descriptor, temporary = tempfile.mkstemp(prefix=".config-protocol-", dir=path.parent)
try:
    with os.fdopen(descriptor, "w", encoding="utf-8") as output:
        json.dump(config, output, ensure_ascii=False, indent=2)
        output.write("\n")
        output.flush(); os.fsync(output.fileno())
    os.chmod(temporary, stat.st_mode & 0o777)
    os.chown(temporary, stat.st_uid, stat.st_gid)
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary): os.unlink(temporary)
PY
        ensure_udp_rule 8443/udp
        ensure_udp_rule 20000/udp
        ensure_udp_rule 20001/udp
        ensure_udp_rule 20002/udp
        systemctl daemon-reload
        systemctl enable --now aimili-xui-protocol-transaction.path aimili-xui-protocol-transaction.timer
        systemctl try-restart aimili-gateway.service
        protocol_stage2_postcheck
        ;;
    3|4|5|6)
        python3 "$VERIFY" preflight --xui-db "$XUI_DB" --require-udp
        ;;
esac

trap - ERR
printf 'stage=%s status=pass backup=%s\n' "$CURRENT_STAGE" "$BACKUP_ROOT"
