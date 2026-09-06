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
if ($statusSource -notmatch 'get\("slots",\s*\[\]\)') { throw 'native status does not count the top-level slots array' }
if ($statusSource -notmatch 'ready.+up') { throw 'native status does not restrict slot counts to ready/up states' }
if ($statusSource -notmatch 'xray-linux-amd64') { throw 'native status does not identify the deployed Xray process name' }
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..')
Push-Location $repoRoot
try {
    & bash 'deploy/local-vm/tests/native-runtime-fixture.tests.sh'
    if ($LASTEXITCODE -ne 0) { throw "native runtime fixture failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
Write-Output 'PASS native verification contract'
