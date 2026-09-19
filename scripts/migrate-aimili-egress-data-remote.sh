#!/usr/bin/env bash
set -Eeuo pipefail

readonly SOURCE_DIR="${AIMILI_EGRESS_OLD_DATA_DIR:-/opt/aimilivpn/vpngate_data}"
readonly TARGET_DIR="${AIMILI_EGRESS_DATA_DIR:-/var/lib/aimili-gateway/aimili-egress}"
readonly UNIT_FILE="${AIMILI_EGRESS_UNIT_FILE:-/etc/systemd/system/aimilivpn.service}"
readonly SERVICE_NAME="${AIMILI_EGRESS_SERVICE_NAME:-aimilivpn.service}"
readonly EXPECTED_SOURCE="/opt/aimili-gateway/services/aimili-egress/vpngate_manager.py"

[[ "$(id -u)" -eq 0 ]] || { printf '%s\n' '必须以 root 运行。' >&2; exit 1; }
[[ -d "$SOURCE_DIR" ]] || { printf '旧数据目录不存在：%s\n' "$SOURCE_DIR" >&2; exit 1; }
[[ -f "$UNIT_FILE" ]] || { printf 'systemd unit 不存在：%s\n' "$UNIT_FILE" >&2; exit 1; }

exec_start="$(systemctl show "$SERVICE_NAME" -p ExecStart --value)"
working_dir="$(systemctl show "$SERVICE_NAME" -p WorkingDirectory --value)"
[[ "$exec_start" == *"$EXPECTED_SOURCE"* ]] || {
    printf '拒绝迁移：当前 ExecStart 不是 Gateway 内置出口引擎：%s\n' "$exec_start" >&2
    exit 1
}
[[ "$working_dir" == "/opt/aimili-gateway/services/aimili-egress" ]] || {
    printf '拒绝迁移：当前 WorkingDirectory 不属于 Gateway：%s\n' "$working_dir" >&2
    exit 1
}

unit_backup="$(mktemp /tmp/aimilivpn.service.XXXXXX)"
cp -a "$UNIT_FILE" "$unit_backup"
service_was_active=0
systemctl is-active --quiet "$SERVICE_NAME" && service_was_active=1

rollback() {
    status=$?
    if [[ "$status" -ne 0 ]]; then
        cp -a "$unit_backup" "$UNIT_FILE"
        systemctl daemon-reload
        if [[ "$service_was_active" -eq 1 ]]; then
            systemctl restart "$SERVICE_NAME" || true
        fi
        printf '%s\n' '迁移失败，已恢复原 unit；旧数据目录始终保留。' >&2
    fi
    rm -f "$unit_backup"
    exit "$status"
}
trap rollback EXIT

install -d -m 0700 "$TARGET_DIR"
systemctl stop "$SERVICE_NAME"
cp -a "$SOURCE_DIR"/. "$TARGET_DIR"/

python3 - "$UNIT_FILE" "$TARGET_DIR" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
target = sys.argv[2]
text = path.read_text(encoding="utf-8")
lines = text.splitlines()
replacement = f"Environment=VPNGATE_DATA_DIR={target}"
matches = [index for index, line in enumerate(lines) if line.startswith("Environment=VPNGATE_DATA_DIR=")]
if len(matches) != 1:
    raise SystemExit(f"unit 中 VPNGATE_DATA_DIR 数量异常：{len(matches)}")
lines[matches[0]] = replacement
path.write_text("\n".join(lines) + "\n", encoding="utf-8")
PY

systemctl daemon-reload
systemctl start "$SERVICE_NAME"
systemctl is-active --quiet "$SERVICE_NAME"

effective_exec="$(systemctl show "$SERVICE_NAME" -p ExecStart --value)"
effective_workdir="$(systemctl show "$SERVICE_NAME" -p WorkingDirectory --value)"
effective_env="$(systemctl show "$SERVICE_NAME" -p Environment --value)"
[[ "$effective_exec" == *"$EXPECTED_SOURCE"* ]]
[[ "$effective_workdir" == "/opt/aimili-gateway/services/aimili-egress" ]]
[[ "$effective_env" == *"VPNGATE_DATA_DIR=$TARGET_DIR"* ]]

main_pid="$(systemctl show "$SERVICE_NAME" -p MainPID --value)"
[[ "$main_pid" =~ ^[1-9][0-9]*$ ]]
if grep -q '/opt/aimilivpn' "/proc/$main_pid/cmdline" 2>/dev/null; then
    printf '%s\n' '迁移后进程命令仍引用旧仓库。' >&2
    exit 1
fi
if ls -l "/proc/$main_pid/fd" 2>/dev/null | grep -q '/opt/aimilivpn'; then
    printf '%s\n' '迁移后进程仍打开旧仓库中的文件。' >&2
    exit 1
fi

printf '迁移完成：源码=%s，数据=%s，PID=%s\n' "$EXPECTED_SOURCE" "$TARGET_DIR" "$main_pid"
