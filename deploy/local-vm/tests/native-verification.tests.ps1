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
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..')
Push-Location $repoRoot
try {
    & bash 'deploy/local-vm/tests/native-runtime-fixture.tests.sh'
    if ($LASTEXITCODE -ne 0) { throw "native runtime fixture failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
Write-Output 'PASS native verification contract'
