[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$path = Join-Path $PSScriptRoot '..\native\install-aimilivpn.sh'
if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw 'AimiliVPN native installer is missing' }
$source = Get-Content -LiteralPath $path -Raw
foreach ($token in @('--check', '--apply', 'MULTI_EXIT_SLOTS', 'MAX_EXIT_SLOTS', 'source-commit', 'aimilivpn.service')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "AimiliVPN installer contract missing: $token" }
}
if ($source -match 'docker|ssh ny|v2rayN') { throw 'AimiliVPN installer contains an out-of-scope integration' }
Write-Output 'PASS AimiliVPN native installer contract'
