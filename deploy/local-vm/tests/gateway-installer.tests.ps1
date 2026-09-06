[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$path = Join-Path $PSScriptRoot '..\native\install-gateway.sh'
if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw 'Gateway native installer is missing' }
$source = Get-Content -LiteralPath $path -Raw
foreach ($token in @('--check', '--apply', 'aimili-gateway.service', 'GATEWAY_CONFIG', 'aimili-gateway.db', 'mixedSourceCidrs')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "Gateway installer contract missing: $token" }
}
if ($source -match 'docker|ssh ny|v2rayN') { throw 'Gateway installer contains an out-of-scope integration' }
Write-Output 'PASS Gateway native installer contract'
