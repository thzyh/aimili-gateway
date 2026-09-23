#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

readonly release_tag='v0.2.0-vps'
readonly release_base="https://github.com/thzyh/aimili-gateway/releases/download/${release_tag}"
readonly public_key_url='https://raw.githubusercontent.com/thzyh/aimili-gateway/main/deploy/vps/release-public.pem'
readonly public_key_sha='850deb6159cce5a6b35806b7269c5f0aea5bfdce9014dac4f7f04bdaf207b831'
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
if d.get('schemaVersion')!=1 or d.get('release')!=sys.argv[2]:raise SystemExit('invalid release manifest')
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
        printf '正在下载 %s……\n' "$name"
        download "$release_base/$name" "$target"
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
exec python3 "$cache/bundle/deploy/vps/installer.py" --asset-root "$cache/bundle" --xui-binary "$cache/$xui_name" "$@"
