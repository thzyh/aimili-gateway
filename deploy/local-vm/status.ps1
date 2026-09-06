[CmdletBinding()]
param([switch]$AsJson)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force
$paths = Get-AimiliLocalVmPaths
$manifestPath = Join-Path $PSScriptRoot 'native\deployment.json'
$manifest = Get-AimiliNativeManifest -Path $manifestPath
$vmrun = Join-Path $paths.VmwareRoot 'vmrun.exe'
$runningPaths = if (Test-Path -LiteralPath $vmrun) { @(& $vmrun list 2>&1) } else { @() }
$running = $runningPaths -contains $paths.VmxPath
$report = [ordered]@{
    vmExists = Test-Path -LiteralPath $paths.VmxPath -PathType Leaf
    vmRunning = $running
    sshReachable = $false
    nativeServices = [ordered]@{ aimilivpn = 'unknown'; xui = 'unknown'; gateway = 'unknown'; caddy = 'unknown' }
    expected = $manifest.expected
    actual = [ordered]@{ openvpn = 0; xray = 0; logicalExits = 0; exitSlots = 0 }
    nativeReady = $false
}
if ($running) {
    $statePath = Join-Path $paths.RuntimeRoot 'state.json'
    $keyPath = Join-Path $paths.RuntimeRoot 'id_ed25519'
    $knownHosts = Join-Path $paths.RuntimeRoot 'known_hosts'
    if ((Test-Path -LiteralPath $statePath) -and (Test-Path -LiteralPath $keyPath)) {
        $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
        & ssh.exe -i $keyPath -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$knownHosts" "aimili@$($state.guestAddress)" true 2>$null
        $report.sshReachable = $LASTEXITCODE -eq 0
        if ($report.sshReachable) {
            $probeCommand = @(
                'printf "svc_aimilivpn=%s\n" "$(systemctl is-active aimilivpn.service 2>/dev/null || true)"',
                'printf "svc_xui=%s\n" "$(systemctl is-active x-ui.service 2>/dev/null || true)"',
                'printf "svc_gateway=%s\n" "$(systemctl is-active aimili-gateway.service 2>/dev/null || true)"',
                'printf "svc_caddy=%s\n" "$(systemctl is-active caddy.service 2>/dev/null || true)"',
                'printf "openvpn=%s\n" "$(pgrep -cx openvpn 2>/dev/null || true)"',
                'printf "xray=%s\n" "$(pgrep -cx xray 2>/dev/null || true)"',
                'printf "logical=%s\n" "$(pgrep -cx openvpn 2>/dev/null || true)"',
                'printf "slots=%s\n" "$(python3 -c ''import json; p="/opt/aimilivpn/vpngate_data/slots.json"; d=json.load(open(p)) if __import__("os").path.exists(p) else {}; print(len(d) if isinstance(d,dict) else len(d))'' 2>/dev/null || true)"'
            ) -join '; '
            $probe = @(& ssh.exe -i $keyPath -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$knownHosts" "aimili@$($state.guestAddress)" $probeCommand 2>$null)
            if ($LASTEXITCODE -eq 0) {
                $values = @{}
                foreach ($line in $probe) {
                    if ([string]$line -match '^([^=]+)=(.*)$') { $values[$Matches[1]] = $Matches[2] }
                }
                $report.nativeServices.aimilivpn = if ($values.ContainsKey('svc_aimilivpn')) { $values.svc_aimilivpn } else { 'unknown' }
                $report.nativeServices.xui = if ($values.ContainsKey('svc_xui')) { $values.svc_xui } else { 'unknown' }
                $report.nativeServices.gateway = if ($values.ContainsKey('svc_gateway')) { $values.svc_gateway } else { 'unknown' }
                $report.nativeServices.caddy = if ($values.ContainsKey('svc_caddy')) { $values.svc_caddy } else { 'unknown' }
                $report.actual.openvpn = if ($values.ContainsKey('openvpn')) { [int]$values.openvpn } else { 0 }
                $report.actual.xray = if ($values.ContainsKey('xray')) { [int]$values.xray } else { 0 }
                $report.actual.logicalExits = if ($values.ContainsKey('logical')) { [int]$values.logical } else { 0 }
                $report.actual.exitSlots = if ($values.ContainsKey('slots')) { [int]$values.slots } else { 0 }
                $report.nativeReady = ($report.nativeServices.Values -notcontains 'unknown' -and
                    @($report.nativeServices.Values | Where-Object { $_ -ne 'active' }).Count -eq 0 -and
                    $report.actual.openvpn -eq $report.expected.openvpn -and
                    $report.actual.xray -eq $report.expected.xray -and
                    $report.actual.logicalExits -eq $report.expected.logicalExits -and
                    $report.actual.exitSlots -eq $report.expected.exitSlots)
            }
        }
    }
}
if ($AsJson) { [pscustomobject]$report | ConvertTo-Json -Compress } else { [pscustomobject]$report }
