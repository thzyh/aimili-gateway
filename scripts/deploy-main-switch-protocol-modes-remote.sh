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
readonly AIMILI_REPOSITORY=/opt/aimilivpn
case "$CURRENT_STAGE" in 1|2|3|4|5|6) ;; *) printf '%s\n' '阶段必须为 1 到 6。' >&2; exit 2 ;; esac
[[ -x /usr/local/x-ui/bin/xray-linux-amd64 && -s "$VERIFY" ]] || { printf '%s\n' '部署资产不完整。' >&2; exit 1; }
[[ -z "$(git -C "$AIMILI_REPOSITORY" status --porcelain)" ]] || { printf '%s\n' 'AimiliVPN 工作区不干净。' >&2; exit 1; }

resource_gate() {
    local memory swap
    memory="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)"
    swap="$(awk '/^SwapFree:/ {print $2}' /proc/meminfo)"
    [[ "$memory" -ge 163840 ]] || { printf '%s\n' 'resource_gate=memory_below_threshold' >&2; return 1; }
    [[ "$swap" -ge 524288 ]] || { printf '%s\n' 'resource_gate=swap_below_threshold' >&2; return 1; }
    printf 'MemAvailable=%sMiB SwapFree=%sMiB\n' "$((memory / 1024))" "$((swap / 1024))"
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
    systemctl show --property MainPID --value x-ui.service > "$BACKUP_ROOT/xray.pid"
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
            cp -a "$BACKUP_ROOT/config.json" /etc/aimili-gateway/config.json
            cp -a "$BACKUP_ROOT/aimili-gateway.db" "$GATEWAY_DB"
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
        git -C "$AIMILI_REPOSITORY" fetch "$ASSET_ROOT/aimili-vpngate.bundle" feature
        git -C "$AIMILI_REPOSITORY" merge --ff-only FETCH_HEAD
        systemctl try-restart aimilivpn.service
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
        python3 "$VERIFY" preflight --xui-db "$XUI_DB" --require-udp
        ;;
    3|4|5|6)
        python3 "$VERIFY" preflight --xui-db "$XUI_DB" --require-udp
        ;;
esac

trap - ERR
printf 'stage=%s status=pass backup=%s\n' "$CURRENT_STAGE" "$BACKUP_ROOT"
