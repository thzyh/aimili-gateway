#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

readonly release_tag='project-v0.2.45-vps'
readonly release_base="https://github.com/thzyh/aimili-gateway/releases/download/${release_tag}"
readonly xui_asset_tag='v0.2.5-vps'
readonly xui_release_base="https://github.com/thzyh/aimili-gateway/releases/download/${xui_asset_tag}"
readonly public_key_url='https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/release-public.pem'
readonly public_key_sha='a67f4f24fb69d0112a3855ac40649d0f80064df98d264a04fd722f46d2cacfbd'
readonly cache="/var/cache/aimili-gateway/${release_tag}"

if (( EUID != 0 )); then
    printf '请以 root 运行，例如：curl -fsSL https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/install.sh | sudo bash\n' >&2
    exit 1
fi
for command in curl openssl python3 sha256sum tar; do
    command -v "$command" >/dev/null 2>&1 || { printf '缺少安装引导依赖：%s\n' "$command" >&2; exit 2; }
done
install -d -m 0700 "$cache"

download() {
    local url="$1" target="$2"
    if [[ ! -s "$target" ]]; then
        curl -fL --retry 3 --retry-delay 2 --connect-timeout 10 --max-time 900 -o "$target.part" "$url"
        mv -f -- "$target.part" "$target"
    fi
}

configure_bbr() {
    # 此步骤属于当前引导入口，不依赖签名包中安装器的版本。
    python3 - <<'AIMILI_BBR'
import os
from pathlib import Path
import subprocess
import tempfile

ETC = Path('/etc')
KEY = 'net.ipv4.tcp_congestion_control'


def command(args):
    result = subprocess.run(args, capture_output=True, text=True, timeout=15)
    if result.returncode:
        reason = (result.stderr or result.stdout).strip()[-600:]
        raise RuntimeError(f"{' '.join(args)}：{reason or '退出码 '+str(result.returncode)}")
    return result.stdout.strip()


def atomic_write(path, body):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, 0o644)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def enable_bbr():
    print('正在检查并默认开启、持久化 BBR（供 3x-ui/Xray 的新 TCP 连接使用）……', flush=True)
    previous = None
    runtime_attempted = False
    changed = []
    try:
        if 'bbr' not in command(['sysctl', '-n', 'net.ipv4.tcp_available_congestion_control']).split():
            command(['modprobe', 'tcp_bbr'])
        if 'bbr' not in command(['sysctl', '-n', 'net.ipv4.tcp_available_congestion_control']).split():
            raise RuntimeError('当前内核加载 tcp_bbr 后仍未提供 BBR')
        previous = command(['sysctl', '-n', KEY])
        configs = {
            ETC / 'modules-load.d/aimili-bbr.conf': b'tcp_bbr\n',
            ETC / 'sysctl.d/zz-aimili-bbr.conf': b'# Aimili Gateway: default for new TCP connections\nnet.ipv4.tcp_congestion_control = bbr\n',
        }
        originals = {path: path.read_bytes() if path.exists() else None for path in configs}
        runtime_attempted = True
        command(['sysctl', '-w', KEY + '=bbr'])
        if command(['sysctl', '-n', KEY]) != 'bbr':
            raise RuntimeError('内核回读不是 bbr，拒绝报告启用成功')
        for path, body in configs.items():
            atomic_write(path, body)
            changed.append(path)
        print('已开启并持久化 BBR；新连接立即使用，旧连接保持原算法，无需重启代理。', flush=True)
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        rollback_errors = []
        for path in reversed(changed):
            try:
                if originals[path] is None:
                    path.unlink(missing_ok=True)
                else:
                    atomic_write(path, originals[path])
            except OSError as rollback_error:
                rollback_errors.append(str(rollback_error))
        if runtime_attempted and previous:
            try:
                command(['sysctl', '-w', KEY + '=' + previous])
            except (OSError, RuntimeError, subprocess.TimeoutExpired) as rollback_error:
                rollback_errors.append(str(rollback_error))
        print(f'警告：BBR 未完成开启和持久化：{error}。继续安装，不把此项报告为成功。', flush=True)
        if rollback_errors:
            print('BBR 恢复未完全成功，请核对 sysctl 与受管配置：' + '；'.join(rollback_errors), flush=True)
        print('处理路径：缺少命令时安装 Ubuntu 官方 procps/kmod；缺少 tcp_bbr 时检查当前内核对应的 '
              'linux-modules-extra-$(uname -r)。受限容器须由宿主开启；无 BBR 的内核须经管理员确认后升级支持 BBR 的内核。'
              '处理完成后按 docs/commands.md 的 BBR 章节重试，勿为此盲目重跑整套安装。', flush=True)
        return False
    return True


if __name__ == '__main__':
    enable_bbr()
AIMILI_BBR
}

printf '正在下载并验证 Aimili Gateway 发布清单……\n'
download "$public_key_url" "$cache/release-public.pem"
[[ "$(sha256sum "$cache/release-public.pem" | cut -d' ' -f1)" == "$public_key_sha" ]] || { printf '发布公钥摘要不匹配。\n' >&2; exit 3; }
curl -fL --retry 3 --connect-timeout 10 --max-time 60 -o "$cache/manifest.json.part" "$release_base/manifest.json"
curl -fL --retry 3 --connect-timeout 10 --max-time 60 -o "$cache/manifest.sig.part" "$release_base/manifest.sig"
mv -f -- "$cache/manifest.json.part" "$cache/manifest.json"
mv -f -- "$cache/manifest.sig.part" "$cache/manifest.sig"
openssl pkeyutl -verify -rawin -pubin -inkey "$cache/release-public.pem" -sigfile "$cache/manifest.sig" -in "$cache/manifest.json" >/dev/null || { printf '发布清单签名无效。\n' >&2; exit 3; }

mapfile -t assets < <(python3 - "$cache/manifest.json" "$release_tag" <<'PY'
import json,re,sys
d=json.load(open(sys.argv[1],encoding='utf-8'))
if d.get('schemaVersion')!=1 or d.get('releaseTag',d.get('release'))!=sys.argv[2] or not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+-vps',d.get('release','')):raise SystemExit('invalid release manifest')
for key in ('package','xui'):
 a=d['assets'][key];name=a['name'];digest=a['sha256']
 if not re.fullmatch(r'[A-Za-z0-9._-]{1,100}',name) or not re.fullmatch(r'[0-9a-f]{64}',digest):raise SystemExit('invalid release asset')
 print(name+' '+digest)
PY
)
[[ ${#assets[@]} -eq 2 ]] || { printf '发布清单缺少资产。\n' >&2; exit 3; }
for asset in "${assets[@]}"; do
    read -r name digest <<< "$asset"
    target="$cache/$name"
    if [[ ! -s "$target" || "$(sha256sum "$target" | cut -d' ' -f1)" != "$digest" ]]; then
        rm -f -- "$target"
        asset_url="$release_base/$name"
        if [[ "$name" == 'x-ui-custom-linux-amd64' ]]; then
            asset_url="$xui_release_base/$name"
            previous="/var/cache/aimili-gateway/${xui_asset_tag}/$name"
            if [[ "$previous" != "$target" && -s "$previous" && "$(sha256sum "$previous" | cut -d' ' -f1)" == "$digest" ]]; then
                cp -- "$previous" "$target"
            fi
        fi
        if [[ ! -s "$target" ]]; then
            printf '正在下载 %s……\n' "$name"
            download "$asset_url" "$target"
        fi
    fi
    [[ "$(sha256sum "$target" | cut -d' ' -f1)" == "$digest" ]] || { printf '发布资产摘要不匹配：%s\n' "$name" >&2; exit 3; }
done

read -r package_name package_digest <<< "${assets[0]}"
read -r xui_name _ <<< "${assets[1]}"
if [[ ! -s "$cache/bundle/deploy/vps/installer.py" || "$(cat "$cache/bundle-digest" 2>/dev/null || true)" != "$package_digest" ]]; then
    rm -rf -- "$cache/bundle"
    install -d -m 0700 "$cache/bundle"
    tar -xzf "$cache/$package_name" -C "$cache/bundle"
    printf '%s\n' "$package_digest" > "$cache/bundle-digest"
fi
# BBR_BOOTSTRAP_BEGIN
configure_network=1
for argument in "$@"; do
    case "$argument" in
        --status|--help|-h) configure_network=0 ;;
    esac
done
if (( configure_network )); then
    configure_bbr
fi
# BBR_BOOTSTRAP_END
exec python3 "$cache/bundle/deploy/vps/installer.py" --asset-root "$cache/bundle" --xui-binary "$cache/$xui_name" "$@"
