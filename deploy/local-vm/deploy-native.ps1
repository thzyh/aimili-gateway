[CmdletBinding()]
param(
    [switch]$PlanOnly,
    [ValidateSet('aimilivpn','xui-caddy','gateway','slots','provision','verify')]
    [string]$ResumeFrom,
    [string]$StatePath = (Join-Path (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AimiliGateway\vmware-local') 'state.json'),
    [string]$ManifestPath = (Join-Path $PSScriptRoot 'native\deployment.json'),
    [string]$AssetRoot = (Join-Path $PSScriptRoot 'native'),
    [string]$GatewayBinary,
    [string]$GatewayAdminBinary,
    [string]$GatewayConfigTemplate,
    [string]$AimiliVpnInstaller,
    [string]$XuiBinary,
    [string]$AimiliVpnSourceCommit,
    [string]$RunId,
    [string]$SshCommand = 'ssh.exe',
    [string]$ScpCommand = 'scp.exe',
    [string]$RuntimeRoot
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

function New-NativeRunId {
    if ($RunId) {
        if ($RunId -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$') { throw 'run_id_invalid' }
        return $RunId
    }
    return 'run-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + ([guid]::NewGuid().ToString('N').Substring(0,8))
}

$state = Read-NativeState -Path $StatePath
$manifest = Get-AimiliNativeManifest -Path $ManifestPath
$stages = @('aimilivpn','xui-caddy','gateway','slots','provision','verify')
if ($ResumeFrom -and -not $RunId) { throw 'resume_run_id_required' }
$run = New-NativeRunId
$resumeIndex = if ($ResumeFrom) { [Array]::IndexOf($stages, $ResumeFrom) } else { 0 }
$paths = Get-AimiliLocalVmPaths
if ($RuntimeRoot) { $paths.RuntimeRoot = $RuntimeRoot }
$checkpointRoot = Join-Path $paths.RuntimeRoot 'deploy-checkpoints'
$checkpointPath = Join-Path $checkpointRoot "$run.json"
$origin = "https://$($state.guestAddress):8080"

if ($ResumeFrom) {
    if ($resumeIndex -lt 0) { throw 'resume_stage_invalid' }
    if ($resumeIndex -gt 0) {
        if (-not (Test-Path -LiteralPath $checkpointPath -PathType Leaf)) { throw 'resume_checkpoint_missing' }
        $checkpoint = Get-Content -LiteralPath $checkpointPath -Raw | ConvertFrom-Json
        $completed = @($checkpoint.completed)
        for ($index = 0; $index -lt $resumeIndex; $index++) {
            if ($completed -notcontains $stages[$index]) { throw "resume_checkpoint_incomplete:$($stages[$index])" }
        }
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

function Invoke-NativeRemote {
    param([string]$Command)
    & $SshCommand @sshOptions $sshTarget $Command
    if ($LASTEXITCODE -ne 0) { throw "native_ssh_failed:$Command" }
}

function Invoke-NativeRemoteInput {
    param([string]$Command, [string]$InputText)
    $InputText | & $SshCommand @sshOptions $sshTarget $Command
    if ($LASTEXITCODE -ne 0) { throw "native_ssh_failed:$Command" }
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
    if (-not $Path -or -not (Test-Path -LiteralPath $Path -PathType Leaf) -or (Get-Item -LiteralPath $Path).Length -le 0) { throw 'native_asset_missing' }
    $name = [IO.Path]::GetFileName($Path)
    if ($name -notmatch '^[A-Za-z0-9._-]+$') { throw 'native_asset_name_invalid' }
    return $name
}

function Test-NativeCheckpointRemote {
    param([string]$Stage)
    switch ($Stage) {
        'aimilivpn' { Invoke-NativeRemote 'sudo -n systemctl is-active --quiet aimilivpn.service' }
        'xui-caddy' { Invoke-NativeRemote 'sudo -n systemctl is-active --quiet x-ui.service && sudo -n systemctl is-active --quiet caddy.service' }
        'gateway' {
            Invoke-NativeRemote 'sudo -n systemctl is-active --quiet aimili-gateway.service && sudo -n systemctl is-active --quiet aimili-xui-protocol-transaction.path && sudo -n systemctl is-active --quiet aimili-xui-protocol-transaction.timer && sudo -n systemctl is-enabled --quiet aimili-gateway.service && sudo -n systemctl is-enabled --quiet aimili-xui-protocol-transaction.path && sudo -n systemctl is-enabled --quiet aimili-xui-protocol-transaction.timer && sudo -n test -f /usr/local/bin/aimili-xui-protocol-transaction && sudo -n test -x /usr/local/bin/aimili-xui-protocol-transaction && sudo -n test -f /usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py && sudo -n test -s /usr/lib/aimili-gateway/aimili_xui_protocol_transaction.py && sudo -n test -f /etc/aimili-gateway/protocol-transaction.json && sudo -n test -s /etc/aimili-gateway/protocol-transaction.json && sudo -n test -f /etc/systemd/system/aimili-xui-protocol-transaction.path && sudo -n test -s /etc/systemd/system/aimili-xui-protocol-transaction.path && sudo -n test -f /etc/systemd/system/aimili-xui-protocol-transaction.service && sudo -n test -s /etc/systemd/system/aimili-xui-protocol-transaction.service && sudo -n test -f /etc/systemd/system/aimili-xui-protocol-transaction.timer && sudo -n test -s /etc/systemd/system/aimili-xui-protocol-transaction.timer'
        }
        'slots' {
            $slotCode = 'import json,sys; rows=json.load(open("/opt/aimilivpn/vpngate_data/slots.json")).get("slots"); n=int(sys.argv[1]); ok=isinstance(rows,list) and len(rows)==n and sorted(int(row.get("slot")) for row in rows)==list(range(n)) and all(isinstance(row,dict) and str(row.get("status","")).lower() in ("ready","up") and row.get("egress_ok") is True for row in rows); raise SystemExit(0 if ok else 1)'
            Invoke-NativeRemote "sudo -n python3 -c '$slotCode' $($manifest.expected.exitSlots)"
        }
        'provision' {
            $provisionCode = 'import sqlite3,sys; db=sqlite3.connect("file:/var/lib/aimili-gateway/aimili-gateway.db?mode=ro",uri=True); n=int(sys.argv[1]); main=db.execute("select count(*) from main_egress where resource_name=''agw-main'' and enabled=1").fetchone()[0]; groups=db.execute("select count(*) from proxy_groups where status=''ready''").fetchone()[0]; modes=db.execute("select count(*) from egress_protocol_modes where state=''ready''").fetchone()[0]; sub=db.execute("select length(subscription_id) from gateway_subscription where id=1 and resource_name=''aimili-gateway-subscription''").fetchone(); raise SystemExit(0 if main==1 and groups==n and modes==n+1 and sub and sub[0]>0 else 1)'
            Invoke-NativeRemote "sudo -n python3 -c '$provisionCode' $($manifest.expected.exitSlots)"
        }
        'verify' { Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/verify-native.sh --json --manifest /etc/aimili-local/deployment.json --evidence /var/lib/aimili-local/verification/native-evidence.json" }
        default { throw 'native_checkpoint_stage_invalid' }
    }
}

if (-not (Test-Path -LiteralPath $AssetRoot -PathType Container)) { throw 'native_asset_root_missing' }
$willRunAimiliVpn = (-not $ResumeFrom -or [Array]::IndexOf($stages, 'aimilivpn') -ge $resumeIndex)
$willRunXui = (-not $ResumeFrom -or [Array]::IndexOf($stages, 'xui-caddy') -ge $resumeIndex)
$willRunGateway = (-not $ResumeFrom -or [Array]::IndexOf($stages, 'gateway') -ge $resumeIndex)
if ($willRunAimiliVpn -and (-not $AimiliVpnSourceCommit -or $AimiliVpnSourceCommit -notmatch '^[0-9a-fA-F]{7,64}$')) { throw 'aimilivpn_source_commit_required' }
if ($willRunXui -and (-not $XuiBinary -or -not (Test-Path -LiteralPath $XuiBinary -PathType Leaf) -or (Get-Item -LiteralPath $XuiBinary).Length -le 0)) { throw 'xui_binary_required' }
if ($willRunGateway) {
    foreach ($required in @($GatewayBinary,$GatewayAdminBinary,$GatewayConfigTemplate)) {
        if (-not $required -or -not (Test-Path -LiteralPath $required -PathType Leaf) -or (Get-Item -LiteralPath $required).Length -le 0) { throw 'gateway_assets_required' }
    }
}
$bundle = Join-Path ([IO.Path]::GetTempPath()) "aimili-native-$run"
if (Test-Path -LiteralPath $bundle) { Remove-Item -LiteralPath $bundle -Recurse -Force }
New-Item -ItemType Directory -Path $bundle -Force | Out-Null
try {
    New-Item -ItemType Directory -Path (Join-Path $bundle 'assets') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $bundle 'deploy\local-vm') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $bundle 'deploy\bin') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $bundle 'deploy\config') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $bundle 'scripts') -Force | Out-Null
    Copy-Item -LiteralPath $AssetRoot -Destination (Join-Path $bundle 'deploy\local-vm\native') -Recurse -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\systemd') -Destination (Join-Path $bundle 'deploy\systemd') -Recurse -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\bin\aimili-gateway-account') -Destination (Join-Path $bundle 'deploy\bin\aimili-gateway-account') -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\bin\aimili-xui-protocol-transaction') -Destination (Join-Path $bundle 'deploy\bin\aimili-xui-protocol-transaction') -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\config\protocol-transaction.example.json') -Destination (Join-Path $bundle 'deploy\config\protocol-transaction.example.json') -Force
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot '..\..\scripts\aimili_xui_protocol_transaction.py') -Destination (Join-Path $bundle 'scripts\aimili_xui_protocol_transaction.py') -Force
    if (-not (Test-Path -LiteralPath $ManifestPath -PathType Leaf)) { throw 'native_manifest_missing' }
    Copy-Item -LiteralPath $ManifestPath -Destination (Join-Path $bundle 'deploy\local-vm\native\deployment.json') -Force
    $assetNames = @{}
    foreach ($item in @($GatewayBinary,$GatewayAdminBinary,$GatewayConfigTemplate,$AimiliVpnInstaller,$XuiBinary)) {
        if ($item) {
            if (-not (Test-Path -LiteralPath $item -PathType Leaf) -or (Get-Item -LiteralPath $item).Length -le 0) { throw "native_asset_missing:$item" }
            $assetName = Get-NativeAssetName $item
            if ($assetNames.ContainsKey($assetName)) { throw "native_asset_basename_collision:$assetName" }
            $assetNames[$assetName] = $true
            Copy-Item -LiteralPath $item -Destination (Join-Path $bundle ('assets\' + $assetName)) -Force
        }
    }
    $manifestFiles = @(Get-ChildItem -LiteralPath $bundle -Recurse -File | Where-Object { $_.Name -ne 'manifest.json' })
    $entries = foreach ($file in $manifestFiles) {
        [ordered]@{ path = [IO.Path]::GetRelativePath($bundle,$file.FullName).Replace('\','/'); sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
    }
    [ordered]@{ schemaVersion = 1; files = $entries } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $bundle 'manifest.json') -Encoding utf8NoBOM
    if (-not $ResumeFrom) {
        Invoke-NativeRemote "rm -rf -- $remoteIncoming && install -d -m 0700 $remoteIncoming"
        & $ScpCommand @sshOptions -r "$bundle\*" "$sshTarget`:$remoteIncoming"
        if ($LASTEXITCODE -ne 0) { throw 'native_scp_failed' }
        Invoke-NativeRemote "sudo -n bash $remoteIncoming/deploy/local-vm/native/stage.sh $remoteIncoming/manifest.json $run"
    } else {
        if ($resumeIndex -gt 0) { Test-NativeCheckpointRemote -Stage $stages[$resumeIndex - 1] }
    }
    $beforeSafety = Get-AimiliHostSafetySnapshot
    $activeComponent = $null
    $manifestRollbackArmed = $false
    try {
        Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component manifest --run-id $run --backup-root $backupRoot"
        $manifestRollbackArmed = ($resumeIndex -eq 0)
        Invoke-NativeRemote "sudo -n install -D -m 0644 $remoteRoot/deploy/local-vm/native/deployment.json /etc/aimili-local/deployment.json"
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'aimilivpn') -ge $resumeIndex) {
            $activeComponent = 'aimilivpn'
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component aimilivpn --run-id $run --backup-root $backupRoot"
            $vpnArgs = "--apply --source-commit $AimiliVpnSourceCommit --slot-count $($manifest.expected.exitSlots) --max-slots $($manifest.expected.exitSlots)"
            if ($AimiliVpnInstaller) { $vpnArgs += " --installer $remoteRoot/assets/$(Get-NativeAssetName $AimiliVpnInstaller)" }
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-aimilivpn.sh $vpnArgs"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-aimilivpn.sh --check --slot-count $($manifest.expected.exitSlots) --max-slots $($manifest.expected.exitSlots)"
            Set-NativeCheckpoint 'aimilivpn'
            $activeComponent = $null
            $manifestRollbackArmed = $false
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'xui-caddy') -ge $resumeIndex) {
            $activeComponent = 'xui-caddy'
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component xui-caddy --run-id $run --backup-root $backupRoot"
            $xuiAsset = "$remoteRoot/assets/$(Get-NativeAssetName $XuiBinary)"
            Invoke-NativeRemote "sudo -n chmod 0755 $xuiAsset"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-xui-caddy.sh --apply --binary $xuiAsset --manifest /etc/aimili-local/deployment.json --allowed-source $($state.allowedSource) --public-origin $origin"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-xui-caddy.sh --check --binary $xuiAsset --manifest /etc/aimili-local/deployment.json --allowed-source $($state.allowedSource) --public-origin $origin"
            Set-NativeCheckpoint 'xui-caddy'
            $activeComponent = $null
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'gateway') -ge $resumeIndex) {
            $activeComponent = 'gateway'
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/backup.sh --component gateway --run-id $run --backup-root $backupRoot"
            Invoke-NativeRemote "sudo -n chmod 0755 $remoteRoot/assets/$(Get-NativeAssetName $GatewayBinary) $remoteRoot/assets/$(Get-NativeAssetName $GatewayAdminBinary)"
            $gatewayArgs = "--apply --binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayBinary) --admin-binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayAdminBinary) --config-template $remoteRoot/assets/$(Get-NativeAssetName $GatewayConfigTemplate) --manifest /etc/aimili-local/deployment.json --allowed-source $($state.allowedSource) --public-origin $origin"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-gateway.sh $gatewayArgs"
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/install-gateway.sh --check --binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayBinary) --admin-binary $remoteRoot/assets/$(Get-NativeAssetName $GatewayAdminBinary) --config-template $remoteRoot/assets/$(Get-NativeAssetName $GatewayConfigTemplate) --manifest /etc/aimili-local/deployment.json --allowed-source $($state.allowedSource) --public-origin $origin"
            Set-NativeCheckpoint 'gateway'
            $activeComponent = $null
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'slots') -ge $resumeIndex) {
            for ($slot = 0; $slot -lt [int]$manifest.expected.exitSlots; $slot++) {
                Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/enable-exits.sh --slot $slot --manifest /etc/aimili-local/deployment.json"
            }
            Set-NativeCheckpoint 'slots'
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'provision') -ge $resumeIndex) {
            # The installer credential is a one-time bootstrap artifact and becomes stale
            # after unified account management changes the password.  Provision with the
            # live AimiliVPN credential file so a resumed deployment also proves that the
            # Gateway, AimiliVPN, and 3x-ui accounts are synchronized.
            Invoke-NativeRemote "sudo -n python3 $remoteRoot/deploy/local-vm/native/provision-gateway.py --manifest /etc/aimili-local/deployment.json --config /etc/aimili-gateway/config.json --credentials /opt/aimilivpn/vpngate_data/ui_auth.json"
            Set-NativeCheckpoint 'provision'
        }
        if (-not $ResumeFrom -or [Array]::IndexOf($stages, 'verify') -ge $resumeIndex) {
            $afterSafety = Get-AimiliHostSafetySnapshot
            Assert-AimiliHostSafetyUnchanged -Before $beforeSafety -After $afterSafety
            $hostSafety = [ordered]@{
                before = [ordered]@{ clientPids = @($beforeSafety.V2rayNPids); proxy = [string]$beforeSafety.Proxy; defaultRoute = [string]$beforeSafety.DefaultRoute }
                after = [ordered]@{ clientPids = @($afterSafety.V2rayNPids); proxy = [string]$afterSafety.Proxy; defaultRoute = [string]$afterSafety.DefaultRoute }
            } | ConvertTo-Json -Depth 5 -Compress
            Invoke-NativeRemoteInput "sudo -n python3 $remoteRoot/deploy/local-vm/native/generate-native-evidence.py --manifest /etc/aimili-local/deployment.json --gateway-db /var/lib/aimili-gateway/aimili-gateway.db --xui-db /etc/x-ui/x-ui.db --public-host $($state.guestAddress) --output /var/lib/aimili-local/verification/native-evidence.json" $hostSafety
            Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/verify-native.sh --json --manifest /etc/aimili-local/deployment.json --evidence /var/lib/aimili-local/verification/native-evidence.json"
            Set-NativeCheckpoint 'verify'
        }
        Assert-AimiliHostSafetyUnchanged -Before $beforeSafety -After (Get-AimiliHostSafetySnapshot)
        $manifestRollbackArmed = $false
        Get-Content -LiteralPath $checkpointPath -Raw
    } catch {
        $originalError = $_.Exception
        $rollbackErrors = @()
        if ($activeComponent) {
            try { Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/rollback.sh --component $activeComponent --run-id $run --backup-root $backupRoot" } catch { $rollbackErrors += $_.Exception.Message }
        }
        if ($manifestRollbackArmed) {
            try { Invoke-NativeRemote "sudo -n bash $remoteRoot/deploy/local-vm/native/rollback.sh --component manifest --run-id $run --backup-root $backupRoot" } catch { $rollbackErrors += $_.Exception.Message }
        }
        if ($rollbackErrors.Count -gt 0) { throw "native_deploy_failed:$($originalError.Message);rollback_failed:$($rollbackErrors -join '|')" }
        throw $originalError
    }
} finally {
    if (Test-Path -LiteralPath $bundle) { Remove-Item -LiteralPath $bundle -Recurse -Force }
}
