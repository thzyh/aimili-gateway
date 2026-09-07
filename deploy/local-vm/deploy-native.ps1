[CmdletBinding()]
param(
    [switch]$PlanOnly,
    [ValidateSet('aimilivpn','xui-caddy','gateway','slots','verify')]
    [string]$ResumeFrom,
    [string]$StatePath = (Join-Path (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AimiliGateway\vmware-local') 'state.json'),
    [string]$ManifestPath = (Join-Path $PSScriptRoot 'native\deployment.json'),
    [string]$AssetRoot = (Join-Path $PSScriptRoot 'native'),
    [string]$GatewayBinary,
    [string]$GatewayAdminBinary,
    [string]$GatewayConfigTemplate,
    [string]$AimiliVpnInstaller,
    [string]$AimiliVpnSourceCommit,
    [string]$RunId
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$modulePath = Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1'
Import-Module $modulePath -Force

function Read-NativeState {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw 'native_state_missing' }
    $state = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
    foreach ($name in @('guestAddress','allowedSource')) {
        if ([string]$state.$name -notmatch '^\d{1,3}(?:\.\d{1,3}){3}$') { throw "native_state_$name`_invalid" }
        $parsed = [Net.IPAddress]::Parse([string]$state.$name)
        if ($parsed.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) { throw "native_state_$name`_invalid" }
    }
    return $state
}

function Get-NativeStages {
    @('aimilivpn','xui-caddy','gateway','slots','verify')
}

function New-NativeRunId {
    if ($RunId) {
        if ($RunId -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$') { throw 'run_id_invalid' }
        return $RunId
    }
    return 'run-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + ([guid]::NewGuid().ToString('N').Substring(0,8))
}

$state = Read-NativeState -Path $StatePath
$manifest = Get-AimiliNativeManifest -Path $ManifestPath
$stages = @(Get-NativeStages)
if ($ResumeFrom -and -not $RunId) { throw 'resume_run_id_required' }
$run = New-NativeRunId
$resumeIndex = if ($ResumeFrom) { [Array]::IndexOf($stages, $ResumeFrom) } else { 0 }
$paths = Get-AimiliLocalVmPaths
$checkpointRoot = Join-Path $paths.RuntimeRoot 'deploy-checkpoints'
$checkpointPath = Join-Path $checkpointRoot "$run.json"
$origin = "https://$($state.guestAddress):8080"

if ($ResumeFrom) {
    if ($resumeIndex -lt 0) { throw 'resume_stage_invalid' }
    if (-not (Test-Path -LiteralPath $checkpointPath -PathType Leaf)) { throw 'resume_checkpoint_missing' }
    $checkpoint = Get-Content -LiteralPath $checkpointPath -Raw | ConvertFrom-Json
    $completed = @($checkpoint.completed)
    for ($index = 0; $index -lt $resumeIndex; $index++) {
        if ($completed -notcontains $stages[$index]) { throw "resume_checkpoint_incomplete:$($stages[$index])" }
    }
}

$plan = [ordered]@{
    mode = if ($PlanOnly) { 'plan' } else { 'apply' }
    runId = $run
    guestAddress = [string]$state.guestAddress
    allowedSource = [string]$state.allowedSource
    publicOrigin = $origin
    expected = $manifest.expected
    stages = $stages
    resumeFrom = if ($ResumeFrom) { $ResumeFrom } else { $null }
    commit = (& git -C (Resolve-Path (Join-Path $PSScriptRoot '..\..')) rev-parse HEAD 2>$null | Select-Object -First 1)
}
if ($PlanOnly) {
    $plan | ConvertTo-Json -Depth 6
    exit 0
}

$keyPath = Join-Path $paths.RuntimeRoot 'id_ed25519'
$knownHosts = Join-Path $paths.RuntimeRoot 'known_hosts'
foreach ($path in @($keyPath,$knownHosts)) { if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw 'native_ssh_material_missing' } }
$sshTarget = "aimili@$($state.guestAddress)"
$sshOptions = @('-i',$keyPath,'-o','BatchMode=yes','-o','ConnectTimeout=15','-o','StrictHostKeyChecking=yes','-o',"UserKnownHostsFile=$knownHosts")
$remoteIncoming = "/tmp/aimili-native-$run"
$remoteRoot = "/var/lib/aimili-local/staging/$run"
$backupRoot = '/var/backups/aimili-local'

function Invoke-NativeSsh {
    param([string[]]$Arguments)
    & ssh.exe @sshOptions $sshTarget @Arguments
    if ($LASTEXITCODE -ne 0) { throw "native_ssh_failed:$($Arguments -join ' ')" }
}

function Invoke-NativeRemote {
    param([string]$Command)
    Invoke-NativeSsh @($Command)
}

function Set-NativeCheckpoint {
    param([string]$Stage)
    New-Item -ItemType Directory -Path $checkpointRoot -Force | Out-Null
    $existing = @()
    if (Test-Path -LiteralPath $checkpointPath -PathType Leaf) { $existing = @((Get-Content -LiteralPath $checkpointPath -Raw | ConvertFrom-Json).completed) }
    if ($existing -notcontains $Stage) { $existing += $Stage }
    [ordered]@{ schemaVersion = 1; runId = $run; completed = $existing } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $checkpointPath -Encoding utf8NoBOM
}

function Get-NativeAssetName {
    param([string]$Path)
    if (-not $Path -or -not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw 'native_asset_missing' }
    $name = [IO.Path]::GetFileName($Path)
    if ($name -notmatch '^[A-Za-z0-9._-]+$') { throw 'native_asset_name_invalid' }
    return $name
}

function Test-NativeCheckpointRemote {
    param([string]$Stage)
    switch ($Stage) {
        'aimilivpn' { Invoke-NativeRemote 'sudo -n systemctl is-active --quiet aimilivpn.service' }
        'xui-caddy' { Invoke-NativeRemote 'sudo -n systemctl is-active --quiet x-ui.service && sudo -n systemctl is-active --quiet caddy.service' }
        'gateway' { Invoke-NativeRemote 'sudo -n systemctl is-active --quiet aimili-gateway.service' }
        'slots' { Invoke-NativeRemote "sudo -n test `$(python3 -c 'import json; print(len(json.load(open(\"/var/lib/aimilivpn/slots.json\"))[\"slots\"]))') -eq $($manifest.expected.exitSlots)" }
        'verify' { Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/verify-native.sh --json --manifest /etc/aimili-local/deployment.json --evidence /var/lib/aimili-local/verification/native-evidence.json" }
        default { throw 'native_checkpoint_stage_invalid' }
    }
}

if (-not (Test-Path -LiteralPath $AssetRoot -PathType Container)) { throw 'native_asset_root_missing' }
$bundle = Join-Path ([IO.Path]::GetTempPath()) "aimili-native-$run"
if (Test-Path -LiteralPath $bundle) { Remove-Item -LiteralPath $bundle -Recurse -Force }
New-Item -ItemType Directory -Path $bundle -Force | Out-Null
try {
    New-Item -ItemType Directory -Path (Join-Path $bundle 'deploy\local-vm') -Force | Out-Null
    Copy-Item -LiteralPath $AssetRoot -Destination (Join-Path $bundle 'deploy\local-vm\native') -Recurse -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\systemd') -Destination (Join-Path $bundle 'deploy\systemd') -Recurse -Force
    foreach ($item in @($GatewayBinary,$GatewayAdminBinary,$GatewayConfigTemplate,$AimiliVpnInstaller)) {
        if ($item) {
            if (-not (Test-Path -LiteralPath $item -PathType Leaf)) { throw "native_asset_missing:$item" }
            Copy-Item -LiteralPath $item -Destination (Join-Path $bundle ('assets\' + [IO.Path]::GetFileName($item))) -Force
        }
    }
    $manifestFiles = @(Get-ChildItem -LiteralPath $bundle -Recurse -File | Where-Object { $_.Name -ne 'manifest.json' })
    $entries = foreach ($file in $manifestFiles) {
        [ordered]@{ path = [IO.Path]::GetRelativePath($bundle,$file.FullName).Replace('\','/'); sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
    }
    [ordered]@{ schemaVersion = 1; files = $entries } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $bundle 'manifest.json') -Encoding utf8NoBOM
    if (-not $ResumeFrom) {
        Invoke-NativeRemote "rm -rf -- $remoteIncoming && install -d -m 0700 $remoteIncoming"
        & scp.exe @sshOptions -r "$bundle\*" "$sshTarget`:$remoteIncoming"
        if ($LASTEXITCODE -ne 0) { throw 'native_scp_failed' }
        Invoke-NativeRemote "sudo -n bash $remoteIncoming/deploy/local-vm/native/stage.sh $remoteIncoming/manifest.json $run"
        Invoke-NativeRemote "sudo -n install -D -m 0644 $remoteRoot/deploy/local-vm/native/deployment.json /etc/aimili-local/deployment.json"
    } else {
        if ($resumeIndex -gt 0) { Test-NativeCheckpointRemote -Stage $stages[$resumeIndex - 1] }
    }
    $beforeSafety = Get-AimiliHostSafetySnapshot
    $activeComponent = $null
    try {
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'aimilivpn') -ge $resumeIndex) {
            $activeComponent = 'aimilivpn'
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component aimilivpn --run-id $run --backup-root $backupRoot"
            if (-not $AimiliVpnSourceCommit -or $AimiliVpnSourceCommit -notmatch '^[0-9a-fA-F]{7,64}$') { throw 'aimilivpn_source_commit_required' }
            $vpnArgs = "--apply --source-commit $AimiliVpnSourceCommit --slot-count $($manifest.expected.exitSlots) --max-slots $($manifest.expected.exitSlots)"
            if ($AimiliVpnInstaller) { $vpnArgs += " --installer $remoteRoot/assets/$(Get-NativeAssetName $AimiliVpnInstaller)" }
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-aimilivpn.sh $vpnArgs"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-aimilivpn.sh --check --slot-count $($manifest.expected.exitSlots) --max-slots $($manifest.expected.exitSlots)"
            Set-NativeCheckpoint 'aimilivpn'
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'xui-caddy') -ge $resumeIndex) {
            $activeComponent = 'xui-caddy'
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component xui-caddy --run-id $run --backup-root $backupRoot"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-xui-caddy.sh --apply --allowed-source $($state.allowedSource) --public-origin $origin"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-xui-caddy.sh --check --allowed-source $($state.allowedSource) --public-origin $origin"
            Set-NativeCheckpoint 'xui-caddy'
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'gateway') -ge $resumeIndex) {
            $activeComponent = 'gateway'
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component gateway --run-id $run --backup-root $backupRoot"
            if (-not $GatewayBinary -or -not $GatewayAdminBinary -or -not $GatewayConfigTemplate) { throw 'gateway_assets_required' }
            $gatewayArgs = "--apply --binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayBinary) --admin-binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayAdminBinary) --config-template $remoteRoot/assets/$(Get-NativeAssetName $GatewayConfigTemplate) --allowed-source $($state.allowedSource) --public-origin $origin"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-gateway.sh $gatewayArgs"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-gateway.sh --check --binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayBinary) --admin-binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayAdminBinary) --config-template $remoteRoot/assets/$(Get-NativeAssetName $GatewayConfigTemplate) --allowed-source $($state.allowedSource) --public-origin $origin"
            Set-NativeCheckpoint 'gateway'
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'slots') -ge $resumeIndex) {
            for ($slot = 0; $slot -lt [int]$manifest.expected.exitSlots; $slot++) {
                Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/enable-exits.sh --slot $slot --manifest /etc/aimili-local/deployment.json"
            }
            Set-NativeCheckpoint 'slots'
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'verify') -ge $resumeIndex) {
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/verify-native.sh --json --manifest /etc/aimili-local/deployment.json --evidence /var/lib/aimili-local/verification/native-evidence.json"
            Set-NativeCheckpoint 'verify'
        }
        Assert-AimiliHostSafetyUnchanged -Before $beforeSafety -After (Get-AimiliHostSafetySnapshot)
        Get-Content -LiteralPath $checkpointPath -Raw
    } catch {
        if ($activeComponent) {
            try { Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/rollback.sh --component $activeComponent --run-id $run --backup-root $backupRoot" } catch { }
        }
        throw
    }
} finally {
    if (Test-Path -LiteralPath $bundle) { Remove-Item -LiteralPath $bundle -Recurse -Force }
}
