#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

readonly XUI_VERSION="v3.7.0"
readonly XUI_ASSET="x-ui-linux-amd64.tar.gz"
readonly XUI_ASSET_SIZE="80280886"
readonly XUI_ASSET_SHA256="0f8dd7baef3458f6591574e24814f322cf7f5e1e27f0a594683745e50be84ec5"
readonly XUI_ASSET_URL="https://github.com/MHSanaei/3x-ui/releases/download/${XUI_VERSION}/${XUI_ASSET}"
readonly BACKUP_ROOT="/var/backups/aimili-gateway/xui-v370"

log() { printf '[xui-v370] %s\n' "$*"; }
die() { printf '[xui-v370] 错误：%s\n' "$*" >&2; exit 1; }

require_root() {
    [[ "$(id -u)" -eq 0 ]] || die "必须使用 root 运行。"
}

current_version() {
    local binary="${1:-/usr/local/x-ui/x-ui}"
    "$binary" -v 2>/dev/null | grep -Eo 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true
}

preflight() {
    require_root
    [[ "$(uname -m)" == "x86_64" ]] || die "只支持 Linux amd64。"
    [[ -x /usr/local/x-ui/x-ui ]] || die "未找到现有 3x-ui 二进制。"
    [[ -f /etc/x-ui/x-ui.db ]] || die "未找到 3x-ui 数据库。"
    [[ -f /etc/systemd/system/x-ui.service || -f /lib/systemd/system/x-ui.service ]] || die "未找到 x-ui systemd unit。"
    command -v curl >/dev/null || die "缺少 curl。"
    command -v sha256sum >/dev/null || die "缺少 sha256sum。"
    command -v tar >/dev/null || die "缺少 tar。"
    local free_kb
    free_kb="$(df -Pk /usr/local | awk 'NR==2 {print $4}')"
    [[ "$free_kb" =~ ^[0-9]+$ ]] || die "无法读取磁盘空间。"
    ((free_kb >= 524288)) || die "可用磁盘不足 512 MiB。"
    systemctl is-active --quiet x-ui || die "x-ui 当前未运行。"
    log "预检通过：架构=amd64，当前版本=$(current_version)，目标版本=${XUI_VERSION}，数据库=存在，服务=active。"
}

download_asset() {
    local destination="$1" actual_size actual_sha
    curl -fL --retry 5 --retry-delay 3 --connect-timeout 15 --speed-limit 1 --speed-time 300 \
        -o "$destination" "$XUI_ASSET_URL"
    actual_size="$(stat -c '%s' "$destination")"
    [[ "$actual_size" == "$XUI_ASSET_SIZE" ]] || die "发布资产大小不匹配。"
    actual_sha="$(sha256sum "$destination" | awk '{print $1}')"
    [[ "$actual_sha" == "$XUI_ASSET_SHA256" ]] || die "发布资产 SHA-256 不匹配。"
    tar -tzf "$destination" | awk '
        BEGIN { ok=1 }
        /^x-ui\// { next }
        { ok=0 }
        END { exit ok ? 0 : 1 }
    ' || die "发布资产包含非预期路径。"
}

backup_current() {
    local backup="$1" unit=""
    install -d -m 0700 "$backup"
    cp -a /etc/x-ui "$backup/etc-x-ui"
    cp -a /usr/local/x-ui "$backup/usr-local-x-ui"
    if [[ -f /etc/systemd/system/x-ui.service ]]; then
        unit=/etc/systemd/system/x-ui.service
    else
        unit=/lib/systemd/system/x-ui.service
    fi
    cp -a "$unit" "$backup/x-ui.service"
    printf '%s\n' "$unit" > "$backup/unit-path"
    chmod -R go-rwx "$backup"
}

restore_backup() {
    local backup="$1" unit_path
    [[ -d "$backup/etc-x-ui" && -d "$backup/usr-local-x-ui" && -f "$backup/x-ui.service" && -f "$backup/unit-path" ]] \
        || die "回滚目录不完整。"
    unit_path="$(<"$backup/unit-path")"
    [[ "$unit_path" == /etc/systemd/system/x-ui.service || "$unit_path" == /lib/systemd/system/x-ui.service ]] \
        || die "回滚 unit 路径无效。"
    systemctl stop x-ui || true
    rm -rf -- /etc/x-ui /usr/local/x-ui
    cp -a "$backup/etc-x-ui" /etc/x-ui
    cp -a "$backup/usr-local-x-ui" /usr/local/x-ui
    cp -a "$backup/x-ui.service" "$unit_path"
    systemctl daemon-reload
    systemctl start x-ui
    systemctl is-active --quiet x-ui || die "回滚后 x-ui 未运行。"
    log "已恢复 3x-ui 二进制、数据库和 unit。"
}

panel_endpoint() {
    local binary="$1" shown port path
    shown="$("$binary" setting -show true 2>/dev/null)" || return 1
    port="$(printf '%s\n' "$shown" | sed -nE 's/^[[:space:]]*port:[[:space:]]*([0-9]+).*$/\1/p' | head -n1)"
    path="$(printf '%s\n' "$shown" | sed -nE 's/^[[:space:]]*webBasePath:[[:space:]]*([^[:space:]]+).*$/\1/p' | head -n1)"
    [[ "$port" =~ ^[0-9]+$ && -n "$path" ]] || return 1
    path="/${path#/}"
    path="${path%/}/"
    printf 'http://127.0.0.1:%s%s' "$port" "${path%/}"
}

wait_for_panel_endpoint() {
    local binary="$1" attempts="${2:-60}" delay="${3:-1}" attempt endpoint
    for ((attempt = 1; attempt <= attempts; attempt++)); do
        if endpoint="$(panel_endpoint "$binary")"; then
            printf '%s' "$endpoint"
            return 0
        fi
        ((attempt == attempts)) || sleep "$delay"
    done
    return 1
}

xray_process_running() {
    local parent_pid="${1:-}"
    if [[ -z "$parent_pid" ]]; then
        parent_pid="$(systemctl show x-ui.service -p MainPID --value 2>/dev/null)"
    fi
    [[ "$parent_pid" =~ ^[1-9][0-9]*$ ]] || return 1
    pgrep -P "$parent_pid" -f '(^|/)xray-linux-amd64([[:space:]]|$)' >/dev/null
}

health_check() {
    local attempt base version
    for attempt in {1..60}; do
        systemctl is-active --quiet x-ui && break
        sleep 1
    done
    systemctl is-active --quiet x-ui || return 1
    base="$(wait_for_panel_endpoint /usr/local/x-ui/x-ui)" || {
        log "健康检查失败阶段=panel-settings"
        return 1
    }
    for attempt in {1..60}; do
        curl -fsS --max-time 2 "$base/csrf-token" >/dev/null 2>&1 && break
        sleep 1
    done
    curl -fsS --max-time 2 "$base/csrf-token" >/dev/null 2>&1 || {
        log "健康检查失败阶段=panel-csrf"
        return 1
    }
    for attempt in {1..60}; do
        xray_process_running && break
        sleep 1
    done
    xray_process_running || {
        log "健康检查失败阶段=xray-process"
        return 1
    }
    version="$(current_version)"
    [[ "$version" == "$XUI_VERSION" || "$version" == "${XUI_VERSION#v}" ]] || {
        log "健康检查失败阶段=version"
        return 1
    }
}

apply_upgrade() {
    preflight
    local stamp work backup staged old
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    work="$(mktemp -d /tmp/xui-v370.XXXXXX)"
    backup="${BACKUP_ROOT}/${stamp}"
    staged="/usr/local/x-ui.new.${stamp}"
    old="/usr/local/x-ui.old.${stamp}"
    trap "rm -rf -- '$work' '$staged'" EXIT
    download_asset "$work/$XUI_ASSET"
    tar -xzf "$work/$XUI_ASSET" -C "$work"
    [[ -x "$work/x-ui/x-ui" && -x "$work/x-ui/bin/xray-linux-amd64" ]] || die "解压后的必要二进制缺失。"
    backup_current "$backup"
    cp -a "$work/x-ui" "$staged"
    cp -an /usr/local/x-ui/bin/. "$staged/bin/" 2>/dev/null || true
    chmod +x "$staged/x-ui" "$staged/x-ui.sh" "$staged/bin/xray-linux-amd64"
    log "已创建整体回滚目录：$backup"
    systemctl stop x-ui
    mv /usr/local/x-ui "$old"
    mv "$staged" /usr/local/x-ui
    if ! systemctl start x-ui || ! health_check; then
        log "升级健康检查失败，开始整体回滚。"
        rm -rf -- /usr/local/x-ui
        mv "$old" /usr/local/x-ui 2>/dev/null || true
        restore_backup "$backup"
        die "升级失败，已恢复原版本。"
    fi
    rm -rf -- "$old"
    log "升级通过：版本=${XUI_VERSION}，服务=active，Xray=running，本机 CSRF=可用。"
}

self_test() {
    local work fake detected xray_pid=""
    [[ "$XUI_VERSION" == "v3.7.0" ]]
    [[ "$XUI_ASSET" == "x-ui-linux-amd64.tar.gz" ]]
    [[ "$XUI_ASSET_SIZE" == "80280886" ]]
    [[ "$XUI_ASSET_SHA256" =~ ^[0-9a-f]{64}$ ]]
    [[ "$XUI_VERSION" != "latest" && "$XUI_VERSION" != "main" && "$XUI_VERSION" != "dev-latest" ]]
    work="$(mktemp -d)"
    trap '[[ -z "${xray_pid:-}" ]] || kill "$xray_pid" 2>/dev/null || true; rm -rf -- "$work"' EXIT
    fake="$work/x-ui"
    printf '%s\n' \
        '#!/usr/bin/env bash' \
        'case "${1:-}" in' \
        '  -v) printf "3.7.0\n" ;;' \
        '  setting)' \
        '    count=0' \
        '    [[ ! -f "$FAKE_XUI_STATE" ]] || count="$(cat "$FAKE_XUI_STATE")"' \
        '    count=$((count + 1))' \
        '    printf "%s\n" "$count" > "$FAKE_XUI_STATE"' \
        '    ((count >= 2)) || exit 42' \
        '    printf "port: 2001\nwebBasePath: /panel/\n"' \
        '    ;;' \
        '  *) exit 42 ;;' \
        'esac' > "$fake"
    chmod +x "$fake"
    detected="$(current_version "$fake")"
    [[ "$detected" == "3.7.0" ]] || die "版本探测必须使用 3x-ui 的 -v 参数。"
    declare -F wait_for_panel_endpoint >/dev/null || die "健康检查必须提供面板设置有界重试。"
    detected="$(FAKE_XUI_STATE="$work/state" wait_for_panel_endpoint "$fake" 2 0)"
    [[ "$detected" == "http://127.0.0.1:2001/panel" ]] || die "面板设置瞬时不可用后未正确恢复。"
    [[ "$(<"$work/state")" == "2" ]] || die "面板设置健康检查没有执行预期重试。"
    install -d "$work/bin"
    ln -s /bin/sleep "$work/bin/xray-linux-amd64"
    (cd "$work" && exec bin/xray-linux-amd64 30) &
    xray_pid=$!
    declare -F xray_process_running >/dev/null || die "健康检查必须识别相对路径启动的 Xray 子进程。"
    xray_process_running "$$" || die "未识别相对路径启动的 Xray 子进程。"
    kill "$xray_pid"
    wait "$xray_pid" 2>/dev/null || true
    xray_pid=""
    rm -rf -- "$work"
    trap - EXIT
    printf 'self-test: ok\n'
}

case "${1:-}" in
    --preflight) [[ $# -eq 1 ]] || die "--preflight 不接受额外参数。"; preflight ;;
    --apply) [[ $# -eq 1 ]] || die "--apply 不接受额外参数。"; apply_upgrade ;;
    --rollback)
        [[ $# -eq 2 ]] || die "用法：--rollback <backup-dir>"
        require_root
        backup="$(readlink -f -- "$2")"
        [[ "$backup" == "$BACKUP_ROOT"/* ]] || die "回滚目录不在固定备份根目录内。"
        restore_backup "$backup"
        ;;
    --self-test) [[ $# -eq 1 ]] || die "--self-test 不接受额外参数。"; self_test ;;
    *) die "用法：$0 --preflight|--apply|--rollback <backup-dir>|--self-test" ;;
esac
