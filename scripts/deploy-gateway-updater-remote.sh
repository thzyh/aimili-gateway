#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-}"
ASSET_INPUT="${2:-}"
if [[ "$ACTION" != "--preflight" && "$ACTION" != "--apply" ]] || [[ -z "$ASSET_INPUT" ]]; then
    echo "usage: deploy-gateway-updater-remote.sh <--preflight|--apply> ASSET_ROOT" >&2
    exit 2
fi
if [[ "$(id -u)" -ne 0 ]]; then
    echo "must run as root" >&2
    exit 1
fi
ASSET_ROOT="$(realpath -- "$ASSET_INPUT")"
[[ -d "$ASSET_ROOT" && "$ASSET_ROOT" != "/" ]]
for required in \
    SHA256SUMS updater.json release-ed25519.pub \
    aimili-gateway-update-fetch aimili-gateway-update-install aimili-gateway-update-rollback \
    aimili-gateway-update-fetch.path aimili-gateway-update-fetch.service \
    aimili-gateway-update-install.path aimili-gateway-update-install.service \
    aimili-gateway.service; do
    [[ -f "$ASSET_ROOT/$required" ]]
done
(cd "$ASSET_ROOT" && sha256sum -c SHA256SUMS)

python3 - "$ASSET_ROOT/updater.json" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
value = json.loads(path.read_text(encoding="utf-8"))
if value.get("allowGatewayInstall") is not False:
    raise SystemExit("allowGatewayInstall must be false during updater bootstrap")
PY

for service_name in aimili-gateway aimilivpn x-ui caddy; do
    [[ "$(systemctl is-active "$service_name.service")" == "active" ]]
done
ROOT_AVAILABLE_KIB="$(df -Pk / | awk 'NR==2 {print $4}')"
[[ "$ROOT_AVAILABLE_KIB" -ge 262144 ]]

if [[ "$ACTION" == "--preflight" ]]; then
    echo "PASS updater preflight"
    exit 0
fi

INSTALL_BACKUP="/var/lib/aimili-gateway-update/install-backup"
[[ "$INSTALL_BACKUP" == /var/lib/aimili-gateway-update/install-backup ]]
install -d -m 0700 -o root -g root /var/lib/aimili-gateway-update
if [[ -e "$INSTALL_BACKUP" ]]; then
    find "$INSTALL_BACKUP" -mindepth 1 -delete
    rmdir "$INSTALL_BACKUP"
fi
install -d -m 0700 -o root -g root "$INSTALL_BACKUP"

backup_one() {
	local target="$1"
    if [[ -e "$target" ]]; then
		install -d -m 0700 -o root -g root "$(dirname "$INSTALL_BACKUP/rootfs$target")"
		cp -a -- "$target" "$INSTALL_BACKUP/rootfs$target"
    fi
}
for item in \
    /usr/local/bin/aimili-gateway-update-fetch \
    /usr/local/bin/aimili-gateway-update-install \
    /usr/local/bin/aimili-gateway-update-rollback \
    /etc/aimili-gateway/updater.json \
    /etc/aimili-gateway/release-ed25519.pub \
    /etc/aimili-gateway/updater-enabled \
    /etc/systemd/system/aimili-gateway-update-fetch.path \
    /etc/systemd/system/aimili-gateway-update-fetch.service \
    /etc/systemd/system/aimili-gateway-update-install.path \
    /etc/systemd/system/aimili-gateway-update-install.service \
    /etc/systemd/system/aimili-gateway.service; do
	backup_one "$item"
done

restore_on_error() {
    local status=$?
    if [[ "$status" -eq 0 ]]; then
        return
    fi
    systemctl disable --now aimili-gateway-update-fetch.path aimili-gateway-update-install.path >/dev/null 2>&1 || true
    while IFS= read -r saved; do
		local target
		target="${saved#"$INSTALL_BACKUP/rootfs"}"
        cp -a -- "$saved" "$target"
	done < <(find "$INSTALL_BACKUP/rootfs" -type f -print 2>/dev/null)
    systemctl daemon-reload || true
    exit "$status"
}
trap restore_on_error EXIT

if ! id -u aimili-gateway-updater >/dev/null 2>&1; then
    useradd --system --home-dir /var/lib/aimili-gateway-update --shell /usr/sbin/nologin aimili-gateway-updater
fi
UPDATER_UID="$(id -u aimili-gateway-updater)"
install -d -m 0750 -o root -g aimili-gateway /var/lib/aimili-gateway/update-spool
install -d -m 0700 -o aimili-gateway -g aimili-gateway /var/lib/aimili-gateway/update-spool/requests
install -d -m 0750 -o root -g aimili-gateway /var/lib/aimili-gateway/update-spool/results
install -d -m 0700 -o aimili-gateway-updater -g aimili-gateway-updater /var/lib/aimili-gateway-update/staging
install -d -m 0750 -o root -g aimili-gateway-updater /etc/aimili-gateway

install -o root -g root -m 0755 "$ASSET_ROOT/aimili-gateway-update-fetch" /usr/local/bin/aimili-gateway-update-fetch
install -o root -g root -m 0755 "$ASSET_ROOT/aimili-gateway-update-install" /usr/local/bin/aimili-gateway-update-install
install -o root -g root -m 0755 "$ASSET_ROOT/aimili-gateway-update-rollback" /usr/local/bin/aimili-gateway-update-rollback
install -o root -g aimili-gateway-updater -m 0640 "$ASSET_ROOT/release-ed25519.pub" /etc/aimili-gateway/release-ed25519.pub

UPDATER_TEMP="$(mktemp /etc/aimili-gateway/.updater.json.XXXXXX)"
python3 - "$ASSET_ROOT/updater.json" "$UPDATER_TEMP" "$UPDATER_UID" <<'PY'
import json, os, pathlib, sys
source, destination, uid = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), int(sys.argv[3])
value = json.loads(source.read_text(encoding="utf-8"))
value["allowGatewayInstall"] = False
value["fetcherUid"] = uid
destination.write_text(json.dumps(value, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")
os.chmod(destination, 0o640)
PY
chown root:aimili-gateway-updater "$UPDATER_TEMP"
mv -f -- "$UPDATER_TEMP" /etc/aimili-gateway/updater.json
rm -f -- /etc/aimili-gateway/updater-enabled

for unit in \
    aimili-gateway-update-fetch.path aimili-gateway-update-fetch.service \
    aimili-gateway-update-install.path aimili-gateway-update-install.service \
    aimili-gateway.service; do
    install -o root -g root -m 0644 "$ASSET_ROOT/$unit" "/etc/systemd/system/$unit"
done
systemd-analyze verify \
    /etc/systemd/system/aimili-gateway-update-fetch.path \
    /etc/systemd/system/aimili-gateway-update-fetch.service \
    /etc/systemd/system/aimili-gateway-update-install.path \
    /etc/systemd/system/aimili-gateway-update-install.service
systemctl daemon-reload
systemctl enable --now aimili-gateway-update-fetch.path aimili-gateway-update-install.path

[[ "$(systemctl is-active aimili-gateway.service)" == "active" ]]
[[ "$(systemctl is-active aimilivpn.service)" == "active" ]]
[[ "$(systemctl is-active x-ui.service)" == "active" ]]
[[ "$(systemctl is-active caddy.service)" == "active" ]]
trap - EXIT
find "$INSTALL_BACKUP" -mindepth 1 -delete
rmdir "$INSTALL_BACKUP"
echo "PASS updater installed disabled"
