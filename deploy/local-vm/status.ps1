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
    nativeEnabled = [ordered]@{ aimilivpn = 'unknown'; xui = 'unknown'; gateway = 'unknown'; caddy = 'unknown' }
    expected = $manifest.expected
    actual = [ordered]@{ openvpn = 0; xray = 0; logicalExits = 0; exitSlots = 0 }
    listeners = [ordered]@{}
    mainChecks = [ordered]@{ tun = $false; route = $false; listener = $false; egress = $false }
    slotChecks = @()
    databaseReadable = $false
    evidenceSchema = $false
    subscriptionExitSet = $false
    protocolIsolation = $false
    hostSafety = $false
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
            $verifyPath = Join-Path $PSScriptRoot 'native\verify-native.sh'
            $verifySource = Get-Content -LiteralPath $verifyPath -Raw
            $probe = @($verifySource | & ssh.exe -i $keyPath -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$knownHosts" "aimili@$($state.guestAddress)" 'sudo -n bash -s -- --json --manifest /etc/aimili-local/deployment.json --evidence /var/lib/aimili-local/verification/native-evidence.json' 2>$null)
            $probeExitCode = $LASTEXITCODE
            $deep = ConvertFrom-AimiliNativeVerifierProbe -Output $probe -ExitCode $probeExitCode
            if ($null -ne $deep) {
                try {
                    $report.nativeServices.aimilivpn = if ($deep.nativeServices.aimilivpn) { 'active' } else { 'inactive' }
                    $report.nativeServices.xui = if ($deep.nativeServices.'x-ui') { 'active' } else { 'inactive' }
                    $report.nativeServices.gateway = if ($deep.nativeServices.'aimili-gateway') { 'active' } else { 'inactive' }
                    $report.nativeServices.caddy = if ($deep.nativeServices.caddy) { 'active' } else { 'inactive' }
                    $report.nativeEnabled.aimilivpn = if ($deep.nativeEnabled.aimilivpn) { 'enabled' } else { 'disabled' }
                    $report.nativeEnabled.xui = if ($deep.nativeEnabled.'x-ui') { 'enabled' } else { 'disabled' }
                    $report.nativeEnabled.gateway = if ($deep.nativeEnabled.'aimili-gateway') { 'enabled' } else { 'disabled' }
                    $report.nativeEnabled.caddy = if ($deep.nativeEnabled.caddy) { 'enabled' } else { 'disabled' }
                    $report.actual = $deep.actual
                    $report.listeners = $deep.listeners
                    $report.mainChecks = $deep.mainChecks
                    $report.slotChecks = @($deep.slotChecks)
                    $report.databaseReadable = [bool]$deep.databaseReadable
                    $report.evidenceSchema = [bool]$deep.evidenceSchema
                    $report.subscriptionExitSet = [bool]$deep.subscriptionExitSet
                    $report.protocolIsolation = [bool]$deep.protocolIsolation
                    $report.hostSafety = [bool]$deep.hostSafety
                    $report.nativeReady = [bool]$deep.nativeReady
                } catch {
                    $report.nativeReady = $false
                }
            }
        }
    }
}
if ($AsJson) { [pscustomobject]$report | ConvertTo-Json -Compress } else { [pscustomobject]$report }
