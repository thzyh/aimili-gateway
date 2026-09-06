[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\native\guest-network-preflight.sh'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    throw 'guest network preflight script is missing'
}

$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($name in @('gatewayReachable', 'publicTcp443', 'dnsResolution', 'httpsReachable', 'ufwOutgoingAllowed', 'failureBoundary')) {
    if ($source -notmatch [regex]::Escape($name)) { throw "network preflight field missing: $name" }
}
if ($source -match 'docker|v2rayN|systemctl\s+stop|ip\s+route\s+(add|del|replace)') {
    throw 'network preflight contains an out-of-scope mutation or Docker probe'
}
Write-Output 'PASS guest network preflight contract'
