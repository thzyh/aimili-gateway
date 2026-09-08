[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\deploy-native.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) { throw 'native deployment entry is missing' }
$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($token in @('PlanOnly', 'ResumeFrom', 'state.json', 'StrictHostKeyChecking=yes', 'stage.sh', 'backup.sh', 'rollback.sh', 'install-aimilivpn.sh', 'install-xui-caddy.sh', 'install-gateway.sh', 'aimili-gateway-account', 'enable-exits.sh', 'provision-gateway.py', 'verify-native.sh')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "native deployment entry missing contract: $token" }
}
foreach ($token in @('row.get("status"', 'list(range(n))', '("ready","up")', 'row.get("egress_ok") is True')) {
    if (-not $source.Contains($token)) { throw "native slot resume check is not exact: $token" }
}
$statePath = Join-Path ([IO.Path]::GetTempPath()) ("aimili-state-{0}.json" -f [guid]::NewGuid().ToString('N'))
try {
    @{ guestAddress = '192.168.88.4'; allowedSource = '192.168.88.1' } | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding utf8NoBOM
    $output = & $scriptPath -PlanOnly -StatePath $statePath
    if ($LASTEXITCODE -ne 0) { throw "native PlanOnly failed with exit code $LASTEXITCODE" }
    $plan = $output | ConvertFrom-Json
    if ($plan.mode -ne 'plan') { throw 'native PlanOnly performed apply' }
    if ($plan.guestAddress -ne '192.168.88.4' -or $plan.allowedSource -ne '192.168.88.1') { throw 'native PlanOnly state mismatch' }
    if ((@($plan.stages) -join ',') -ne 'aimilivpn,xui-caddy,gateway,slots,provision,verify') { throw 'native deployment stage order changed' }
} finally {
    if (Test-Path -LiteralPath $statePath) { Remove-Item -LiteralPath $statePath -Force }
}

function New-DeployFixtureAsset {
    param([string]$Name)
    $path = Join-Path ([IO.Path]::GetTempPath()) ("aimili-asset-{0}-{1}" -f ([guid]::NewGuid().ToString('N'), $Name))
    [IO.File]::WriteAllText($path, "fixture-$Name")
    return $path
}

$deployState = Join-Path ([IO.Path]::GetTempPath()) ("aimili-apply-state-{0}.json" -f [guid]::NewGuid().ToString('N'))
$deployLog = Join-Path ([IO.Path]::GetTempPath()) ("aimili-apply-log-{0}.txt" -f [guid]::NewGuid().ToString('N'))
$fakeSsh = Join-Path ([IO.Path]::GetTempPath()) ("aimili-fake-ssh-{0}.ps1" -f [guid]::NewGuid().ToString('N'))
$fakeScp = Join-Path ([IO.Path]::GetTempPath()) ("aimili-fake-scp-{0}.ps1" -f [guid]::NewGuid().ToString('N'))
$runtime = Join-Path ([IO.Path]::GetTempPath()) ("aimili-runtime-{0}" -f [guid]::NewGuid().ToString('N'))
$fakeRemote = Join-Path ([IO.Path]::GetTempPath()) ("aimili-fake-remote-{0}" -f [guid]::NewGuid().ToString('N'))
$keyPath = Join-Path $runtime 'id_ed25519'
$hostsPath = Join-Path $runtime 'known_hosts'
$assets = @()
try {
    New-Item -ItemType Directory -Path $runtime,$fakeRemote -Force | Out-Null
    Set-Content -LiteralPath $keyPath -Value 'fixture-key' -Encoding ascii
    Set-Content -LiteralPath $hostsPath -Value 'fixture-host' -Encoding ascii
    @{ guestAddress = '192.168.88.4'; allowedSource = '192.168.88.1' } | ConvertTo-Json | Set-Content -LiteralPath $deployState -Encoding utf8NoBOM
    $gatewayAsset = New-DeployFixtureAsset 'gateway'
    $adminAsset = New-DeployFixtureAsset 'admin'
    $configAsset = New-DeployFixtureAsset 'config'
    $vpnAsset = New-DeployFixtureAsset 'vpn'
    $xuiAsset = New-DeployFixtureAsset 'xui-custom'
    $assets = @($gatewayAsset, $adminAsset, $configAsset, $vpnAsset, $xuiAsset)
    Set-Content -LiteralPath $fakeSsh -Value @'
@($args) | Out-String | Add-Content -LiteralPath $env:NATIVE_DEPLOY_LOG
$command = [string]$args[-1]
if ($env:NATIVE_DEPLOY_FAILURE -and $command -match [regex]::Escape($env:NATIVE_DEPLOY_FAILURE)) { exit 42 }
if ($env:NATIVE_ROLLBACK_FAIL -eq '1' -and $command -match 'rollback\.sh') { exit 43 }
$incoming = Join-Path $env:NATIVE_DEPLOY_REMOTE 'incoming'
if ($command -match '^rm -rf -- /tmp/aimili-native-') {
    if (Test-Path -LiteralPath $incoming) { Remove-Item -LiteralPath $incoming -Recurse -Force }
    New-Item -ItemType Directory -Path $incoming -Force | Out-Null
} elseif ($command -match 'stage\.sh .* (?<run>[A-Za-z0-9._-]+)$') {
    $manifestPath = Join-Path $incoming 'manifest.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { exit 50 }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    foreach ($entry in @($manifest.files)) {
        $source = Join-Path $incoming ([string]$entry.path).Replace('/', [IO.Path]::DirectorySeparatorChar)
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { exit 51 }
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -ne [string]$entry.sha256) { exit 52 }
    }
    $staging = Join-Path $env:NATIVE_DEPLOY_REMOTE ('staging\' + $Matches.run)
    New-Item -ItemType Directory -Path $staging -Force | Out-Null
    Copy-Item -Path (Join-Path $incoming '*') -Destination $staging -Recurse -Force
} elseif ($command -match 'backup\.sh --component (?<component>[a-z0-9-]+) --run-id (?<run>[A-Za-z0-9._-]+)') {
    $marker = Join-Path $env:NATIVE_DEPLOY_REMOTE ('backups\' + $Matches.run + '\' + $Matches.component + '.json')
    New-Item -ItemType Directory -Path (Split-Path $marker) -Force | Out-Null
    Set-Content -LiteralPath $marker -Value '{}' -Encoding ascii
} elseif ($command -match 'install -D -m 0644 /var/lib/aimili-local/staging/(?<run>[^/]+)/deploy/local-vm/native/deployment\.json') {
    $source = Join-Path $env:NATIVE_DEPLOY_REMOTE ('staging\' + $Matches.run + '\deploy\local-vm\native\deployment.json')
    $destination = Join-Path $env:NATIVE_DEPLOY_REMOTE 'etc\deployment.json'
    New-Item -ItemType Directory -Path (Split-Path $destination) -Force | Out-Null
    Copy-Item -LiteralPath $source -Destination $destination -Force
} elseif ($command -match 'rollback\.sh --component (?<component>[a-z0-9-]+) --run-id (?<run>[A-Za-z0-9._-]+)') {
    $marker = Join-Path $env:NATIVE_DEPLOY_REMOTE ('backups\' + $Matches.run + '\' + $Matches.component + '.json')
    if (-not (Test-Path -LiteralPath $marker -PathType Leaf)) { exit 53 }
} elseif ($command -match '/var/lib/aimili-local/staging/(?<run>[^/]+)/') {
    if (-not (Test-Path -LiteralPath (Join-Path $env:NATIVE_DEPLOY_REMOTE ('staging\' + $Matches.run)))) { exit 54 }
}
exit 0
'@ -Encoding utf8NoBOM
    Set-Content -LiteralPath $fakeScp -Value @'
@($args) | Out-String | Add-Content -LiteralPath $env:NATIVE_DEPLOY_LOG
$sourcePattern = [string]$args[-2]
$sourceRoot = Split-Path $sourcePattern
$incoming = Join-Path $env:NATIVE_DEPLOY_REMOTE 'incoming'
New-Item -ItemType Directory -Path $incoming -Force | Out-Null
Copy-Item -Path (Join-Path $sourceRoot '*') -Destination $incoming -Recurse -Force
exit 0
'@ -Encoding utf8NoBOM
    $env:NATIVE_DEPLOY_LOG = $deployLog
    $env:NATIVE_DEPLOY_REMOTE = $fakeRemote
    $env:NATIVE_DEPLOY_FAILURE = ''

    & $scriptPath -StatePath $deployState -RuntimeRoot $runtime -SshCommand $fakeSsh -ScpCommand $fakeScp -GatewayBinary $gatewayAsset -GatewayAdminBinary $adminAsset -GatewayConfigTemplate $configAsset -AimiliVpnInstaller $vpnAsset -XuiBinary $xuiAsset -AimiliVpnSourceCommit 'edd08172a2ce132f2e1525d7e00b047a56883ff9' -RunId 'apply-success' | Out-Null
    $successLog = Get-Content -LiteralPath $deployLog -Raw
    if ($successLog -notmatch 'assets' -or $successLog -notmatch 'enable-exits\.sh --slot 0' -or $successLog -notmatch 'provision-gateway\.py' -or $successLog -notmatch 'verify-native\.sh') { throw "native apply did not package assets or complete ordered stages: $successLog" }
    if ($successLog -notmatch 'install-xui-caddy\.sh --apply .*--binary /var/lib/aimili-local/staging/apply-success/assets/.+xui-custom') { throw 'native apply did not install the staged custom x-ui binary' }
    if ($successLog.IndexOf('provision-gateway.py') -gt $successLog.IndexOf('generate-native-evidence.py')) { throw 'native evidence ran before Gateway provisioning' }
    foreach ($relative in @('staging\apply-success\assets','staging\apply-success\scripts\aimili_xui_protocol_transaction.py','staging\apply-success\deploy\bin\aimili-xui-protocol-transaction','etc\deployment.json')) {
        if (-not (Test-Path -LiteralPath (Join-Path $fakeRemote $relative))) { throw "fake remote did not receive or stage bundle asset: $relative" }
    }
    $manifestBackup = $successLog.IndexOf('backup.sh --component manifest')
    $manifestInstall = $successLog.IndexOf('install -D -m 0644')
    if ($manifestBackup -lt 0 -or $manifestInstall -lt 0 -or $manifestBackup -gt $manifestInstall) { throw 'deployment manifest was not backed up before mutation' }

    Remove-Item -LiteralPath $deployLog -Force
    $env:NATIVE_DEPLOY_FAILURE = 'install-xui-caddy.sh --apply'
    try {
        & $scriptPath -StatePath $deployState -RuntimeRoot $runtime -SshCommand $fakeSsh -ScpCommand $fakeScp -GatewayBinary $gatewayAsset -GatewayAdminBinary $adminAsset -GatewayConfigTemplate $configAsset -AimiliVpnInstaller $vpnAsset -XuiBinary $xuiAsset -AimiliVpnSourceCommit 'edd08172a2ce132f2e1525d7e00b047a56883ff9' -RunId 'apply-xui-fail' | Out-Null
        throw 'x-ui failure was not surfaced'
    } catch {
        if ($_.Exception.Message -notmatch 'native_ssh_failed') { throw }
    }
    $failureLog = Get-Content -LiteralPath $deployLog -Raw
    if ($failureLog -notmatch 'rollback\.sh --component xui-caddy' -or $failureLog -match 'rollback\.sh --component manifest|install-gateway|enable-exits|verify-native') { throw 'x-ui failure did not preserve the committed manifest while rolling back only the active component' }

    Remove-Item -LiteralPath $deployLog -Force
    $env:NATIVE_DEPLOY_FAILURE = 'install-aimilivpn.sh --apply'
    try {
        & $scriptPath -StatePath $deployState -RuntimeRoot $runtime -SshCommand $fakeSsh -ScpCommand $fakeScp -GatewayBinary $gatewayAsset -GatewayAdminBinary $adminAsset -GatewayConfigTemplate $configAsset -AimiliVpnInstaller $vpnAsset -XuiBinary $xuiAsset -AimiliVpnSourceCommit 'edd08172a2ce132f2e1525d7e00b047a56883ff9' -RunId 'apply-aimili-fail' | Out-Null
        throw 'AimiliVPN failure was not surfaced'
    } catch {
        if ($_.Exception.Message -notmatch 'native_ssh_failed') { throw }
    }
    $firstFailureLog = Get-Content -LiteralPath $deployLog -Raw
    if ($firstFailureLog -notmatch 'rollback\.sh --component aimilivpn' -or $firstFailureLog -notmatch 'rollback\.sh --component manifest' -or $firstFailureLog -match 'install-xui-caddy|install-gateway|enable-exits|verify-native') { throw 'first component failure did not rollback the active component and uncommitted manifest' }

    $env:NATIVE_DEPLOY_FAILURE = ''
    & $scriptPath -StatePath $deployState -RuntimeRoot $runtime -SshCommand $fakeSsh -ScpCommand $fakeScp -GatewayBinary $gatewayAsset -GatewayAdminBinary $adminAsset -GatewayConfigTemplate $configAsset -AimiliVpnInstaller $vpnAsset -XuiBinary $xuiAsset -AimiliVpnSourceCommit 'edd08172a2ce132f2e1525d7e00b047a56883ff9' -RunId 'apply-xui-fail' -ResumeFrom xui-caddy | Out-Null

    foreach ($missingGatewayDependency in @('aimili-xui-protocol-transaction.path','aimili-xui-protocol-transaction.timer','/usr/local/bin/aimili-xui-protocol-transaction')) {
        $env:NATIVE_DEPLOY_FAILURE = $missingGatewayDependency
        try {
            & $scriptPath -StatePath $deployState -RuntimeRoot $runtime -SshCommand $fakeSsh -ScpCommand $fakeScp -RunId 'apply-success' -ResumeFrom slots | Out-Null
            throw "gateway resume accepted missing remote dependency: $missingGatewayDependency"
        } catch {
            if ($_.Exception.Message -notmatch 'native_ssh_failed') { throw }
        }
    }

    Remove-Item -LiteralPath $deployLog -Force
    $env:NATIVE_DEPLOY_FAILURE = 'install-xui-caddy.sh --apply'
    $env:NATIVE_ROLLBACK_FAIL = '1'
    try {
        & $scriptPath -StatePath $deployState -RuntimeRoot $runtime -SshCommand $fakeSsh -ScpCommand $fakeScp -GatewayBinary $gatewayAsset -GatewayAdminBinary $adminAsset -GatewayConfigTemplate $configAsset -AimiliVpnInstaller $vpnAsset -XuiBinary $xuiAsset -AimiliVpnSourceCommit 'edd08172a2ce132f2e1525d7e00b047a56883ff9' -RunId 'apply-rollback-fail' | Out-Null
        throw 'rollback failure was not surfaced'
    } catch {
        if ($_.Exception.Message -notmatch 'rollback_failed') { throw }
    }
} finally {
    Remove-Item Env:NATIVE_ROLLBACK_FAIL -ErrorAction SilentlyContinue
    Remove-Item Env:NATIVE_DEPLOY_LOG,Env:NATIVE_DEPLOY_FAILURE,Env:NATIVE_DEPLOY_REMOTE -ErrorAction SilentlyContinue
    foreach ($asset in $assets) { if (Test-Path -LiteralPath $asset) { Remove-Item -LiteralPath $asset -Force } }
    foreach ($path in @($deployState,$deployLog,$fakeSsh,$fakeScp)) { if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force } }
    if (Test-Path -LiteralPath $runtime) { Remove-Item -LiteralPath $runtime -Recurse -Force }
    if (Test-Path -LiteralPath $fakeRemote) { Remove-Item -LiteralPath $fakeRemote -Recurse -Force }
}
$global:LASTEXITCODE = 0
Write-Output 'PASS native deployment entry contract'
