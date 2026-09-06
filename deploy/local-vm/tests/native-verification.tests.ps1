[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
foreach ($name in @('enable-exits.sh', 'verify-native.sh')) {
    $path = Join-Path $PSScriptRoot "..\native\$name"
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "native verification script is missing: $name" }
    $source = Get-Content -LiteralPath $path -Raw
    if ($source -match 'docker|ssh ny|v2rayN') { throw "native verification script contains an out-of-scope integration: $name" }
    if ($source -notmatch 'deployment.json|manifest') { throw "native verification script does not consume a manifest: $name" }
}
$statusPath = Join-Path $PSScriptRoot '..\status.ps1'
$statusSource = Get-Content -LiteralPath $statusPath -Raw
if ($statusSource -notmatch 'verify-native\.sh') { throw 'native status does not reuse deep native verification' }
if ($statusSource -notmatch 'subscriptionExitSet|protocolIsolation|hostSafety') { throw 'native status omits deep verification evidence fields' }
if ($statusSource -match '\$report\.nativeReady\s*=\s*\(\$report\.nativeServices') { throw 'native status still computes shallow readiness from services and counts' }
$parseErrors = $null
$statusAst = [System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path $statusPath), [ref]$null, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) { throw 'native status does not parse' }
$remoteCommands = @($statusAst.FindAll({
    param($node)
    $node -is [System.Management.Automation.Language.CommandAst] -and
        $node.GetCommandName() -eq 'ssh.exe' -and
        (($node.CommandElements | ForEach-Object { $_.Extent.Text }) -join ' ') -match 'verify-native|bash -s'
}, $true))
if ($remoteCommands.Count -ne 1) { throw 'native status verifier SSH command was not captured exactly once' }
$capturedRemoteCommand = ($remoteCommands[0].CommandElements | ForEach-Object { $_.Extent.Text }) -join ' '
if ($capturedRemoteCommand -notmatch "'sudo -n bash -s -- --json --manifest /etc/aimili-local/deployment\.json --evidence /var/lib/aimili-local/verification/native-evidence\.json'") {
    throw 'native status verifier SSH command does not use non-interactive sudo'
}
$modulePath = Join-Path $PSScriptRoot '..\lib\AimiliLocalVm.psm1'
Import-Module $modulePath -Force
$diagnosticJson = [ordered]@{
    nativeServices = [ordered]@{ aimilivpn = $true; 'x-ui' = $true; 'aimili-gateway' = $false; caddy = $true }
    nativeEnabled = [ordered]@{ aimilivpn = $true; 'x-ui' = $true; 'aimili-gateway' = $true; caddy = $true }
    expected = [ordered]@{ openvpn = 4; xray = 1; logicalExits = 4; exitSlots = 3 }
    actual = [ordered]@{ openvpn = 3; xray = 1; logicalExits = 3; exitSlots = 2 }
    listeners = [ordered]@{ gateway = $false }
    mainChecks = [ordered]@{ tun = $true; route = $true; listener = $true; egress = $true }
    slotChecks = @([ordered]@{ slot = 0; ready = $true; tun = $true; route = $true; listener = $true; egress = $true })
    databaseReadable = $true
    evidenceSchema = $true
    subscriptionExitSet = $true
    protocolIsolation = $true
    hostSafety = $true
    nativeReady = $true
} | ConvertTo-Json -Depth 8 -Compress
$unhealthy = ConvertFrom-AimiliNativeVerifierProbe -Output @($diagnosticJson) -ExitCode 1
if ($null -eq $unhealthy -or [int]$unhealthy.actual.openvpn -ne 3 -or [bool]$unhealthy.nativeServices.'aimili-gateway' -or [bool]$unhealthy.nativeReady) {
    throw 'native status discarded a valid unhealthy verifier diagnostic'
}
if ($null -ne (ConvertFrom-AimiliNativeVerifierProbe -Output @($diagnosticJson) -ExitCode 2)) {
    throw 'native status trusted verifier output from an SSH or sudo failure'
}
if ($null -ne (ConvertFrom-AimiliNativeVerifierProbe -Output @('{not-json') -ExitCode 1)) {
    throw 'native status trusted malformed verifier JSON'
}
if ($null -ne (ConvertFrom-AimiliNativeVerifierProbe -Output @('{"nativeReady":false}') -ExitCode 1)) {
    throw 'native status trusted an incomplete verifier report'
}
if ($statusSource -notmatch 'ConvertFrom-AimiliNativeVerifierProbe') { throw 'native status does not use the verifier probe boundary' }
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..')
Push-Location $repoRoot
try {
    & bash 'deploy/local-vm/tests/native-runtime-fixture.tests.sh'
    if ($LASTEXITCODE -ne 0) { throw "native runtime fixture failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
Write-Output 'PASS native verification contract'
