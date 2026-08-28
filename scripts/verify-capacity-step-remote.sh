#!/usr/bin/env bash
set -Eeuo pipefail

readonly MEM_MIN_KB=81920
readonly SWAP_MAX_KB=524288
readonly SWAP_GROWTH_MAX_KB=65536
readonly SAMPLE_INTERVAL=15
LOW_MEMORY_STREAK=0

log() { printf '[capacity] %s\n' "$*"; }
die() { printf '[capacity] 错误：%s\n' "$*" >&2; exit 1; }

evaluate_thresholds() {
    local mem_kb="$1" swap_used_kb="$2" swap_growth_kb="$3" restart_growth="$4" failed_units="$5" oom="$6" ssh_ok="$7"
    if ((mem_kb < MEM_MIN_KB)); then
        LOW_MEMORY_STREAK=$((LOW_MEMORY_STREAK + 1))
    else
        LOW_MEMORY_STREAK=0
    fi
    ((LOW_MEMORY_STREAK < 2)) || return 1
    ((swap_used_kb <= SWAP_MAX_KB)) || return 1
    ((swap_growth_kb <= SWAP_GROWTH_MAX_KB)) || return 1
    ((restart_growth == 0)) || return 1
    ((failed_units == 0)) || return 1
    ((oom == 0)) || return 1
    ((ssh_ok == 1)) || return 1
    return 0
}

self_test() {
    local work legacy live ready
    LOW_MEMORY_STREAK=0
    evaluate_thresholds 81000 0 0 0 0 0 1 || die "首次低内存样本不应立即失败。"
    ! evaluate_thresholds 80000 0 0 0 0 0 1 || die "连续两次低内存未被拒绝。"
    for field in swap growth restart failed oom ssh; do
        LOW_MEMORY_STREAK=0
        case "$field" in
            swap) ! evaluate_thresholds 100000 524289 0 0 0 0 1 ;;
            growth) ! evaluate_thresholds 100000 100000 65537 0 0 0 1 ;;
            restart) ! evaluate_thresholds 100000 0 0 1 0 0 1 ;;
            failed) ! evaluate_thresholds 100000 0 0 0 1 0 1 ;;
            oom) ! evaluate_thresholds 100000 0 0 0 0 1 1 ;;
            ssh) ! evaluate_thresholds 100000 0 0 0 0 0 0 ;;
        esac || die "门槛自测失败：$field"
    done
    work="$(mktemp -d)"
    trap 'rm -rf -- "$work"' EXIT
    legacy="$work/legacy.db"
    live="$work/live.db"
    python3 - "$legacy" "$live" <<'PY'
import sqlite3
import sys

with sqlite3.connect(sys.argv[1]) as db:
    db.execute("create table legacy_marker(id integer primary key)")
with sqlite3.connect(sys.argv[2]) as db:
    db.execute("create table proxy_groups(status text not null)")
    db.executemany("insert into proxy_groups(status) values (?)", [("ready",), ("ready",), ("degraded",)])
PY
    ready="$(ready_groups "$legacy" "$live")"
    [[ "$ready" == "2" ]] || die "容量计数必须跳过不含代理组表的旧数据库。"
    rm -rf -- "$work"
    trap - EXIT
    printf 'self-test: ok\n'
}

restart_total() {
    local total=0 value service
    for service in aimili-gateway.service aimilivpn.service x-ui.service caddy.service; do
        value="$(systemctl show "$service" -p NRestarts --value 2>/dev/null || printf '0')"
        [[ "$value" =~ ^[0-9]+$ ]] || value=0
        total=$((total + value))
    done
    printf '%s' "$total"
}

service_failures() {
    local count=0 service
    for service in aimili-gateway.service aimilivpn.service x-ui.service caddy.service; do
        systemctl is-active --quiet "$service" || count=$((count + 1))
    done
    count=$((count + $(systemctl --failed --no-legend --plain 2>/dev/null | sed '/^[[:space:]]*$/d' | wc -l)))
    printf '%s' "$count"
}

ssh_probe() {
    local port
    port="$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2; exit}')"
    [[ "$port" =~ ^[0-9]+$ ]] || port=22
    timeout 5 bash -c "exec 3<>/dev/tcp/127.0.0.1/$port" >/dev/null 2>&1
}

ready_groups() {
    python3 - "$@" <<'PY' 2>/dev/null || printf '0'
import os
import sqlite3
import sys

paths = tuple(sys.argv[1:]) or (
    "/var/lib/aimili-gateway/gateway.db",
    "/var/lib/aimili-gateway/aimili-gateway.db",
)
for path in paths:
    if os.path.isfile(path):
        try:
            with sqlite3.connect(f"file:{path}?mode=ro", uri=True) as db:
                print(db.execute("select count(*) from proxy_groups where status = 'ready'").fetchone()[0])
            break
        except sqlite3.Error:
            continue
else:
    print(0)
PY
}

run_verification() {
    [[ "$(id -u)" -eq 0 ]] || die "必须使用 root 运行。"
    local stage_start="$1" expected="$2" duration="${3:-900}"
    [[ "$stage_start" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || die "阶段开始时间必须是 UTC ISO-8601。"
    [[ "$expected" =~ ^[1-3]$ ]] || die "预期容量只能是 1、2 或 3。"
    [[ "$duration" =~ ^[0-9]+$ && "$duration" -ge 60 ]] || die "采样时长至少 60 秒。"

    local baseline_restart baseline_swap start_epoch now elapsed mem_kb swap_total swap_free swap_used swap_growth
    local restarts restart_growth failed oom ssh_ok openvpn ready
    baseline_restart="$(restart_total)"
    baseline_swap="$(awk '/^SwapTotal:/ {total=$2} /^SwapFree:/ {free=$2} END {print total-free}' /proc/meminfo)"
    start_epoch="$(date +%s)"
    LOW_MEMORY_STREAK=0
    while true; do
        now="$(date +%s)"
        elapsed=$((now - start_epoch))
        mem_kb="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)"
        read -r swap_total swap_free < <(awk '/^SwapTotal:/ {total=$2} /^SwapFree:/ {free=$2} END {print total, free}' /proc/meminfo)
        swap_used=$((swap_total - swap_free))
        swap_growth=0
        ((elapsed < 300 || swap_used <= baseline_swap)) || swap_growth=$((swap_used - baseline_swap))
        restarts="$(restart_total)"
        restart_growth=$((restarts - baseline_restart))
        failed="$(service_failures)"
        oom=0
        journalctl -k --since "$stage_start" --no-pager 2>/dev/null | grep -Eqi 'out of memory|oom-kill|killed process' && oom=1
        ssh_ok=0
        ssh_probe && ssh_ok=1
        openvpn="$(pgrep -fc 'openvpn' || true)"
        ready="$(ready_groups)"
        [[ "$ready" =~ ^[0-9]+$ ]] || ready=0
        log "elapsed=${elapsed}s mem_available_kb=${mem_kb} swap_used_kb=${swap_used} restarts=${restarts} failed=${failed} openvpn=${openvpn} ready=${ready} ssh=${ssh_ok} oom=${oom}"
        evaluate_thresholds "$mem_kb" "$swap_used" "$swap_growth" "$restart_growth" "$failed" "$oom" "$ssh_ok" \
            || die "容量阶段触发资源或稳定性回退门槛。"
        ((ready >= expected)) || die "ready 组数量低于预期容量。"
        ((elapsed >= duration)) && break
        sleep "$SAMPLE_INTERVAL"
    done
    log "PASS expected_capacity=${expected} duration_seconds=${duration}"
}

if [[ "${1:-}" == "--self-test" ]]; then
    [[ $# -eq 1 ]] || die "--self-test 不接受额外参数。"
    self_test
    exit 0
fi
[[ $# -ge 2 && $# -le 3 ]] || die "用法：$0 <stage-start-utc> <expected-capacity> [duration-seconds]"
run_verification "$@"
