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
Write-Output 'PASS native verification contract'
