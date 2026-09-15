[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$path = Join-Path $PSScriptRoot '..\native\install-gateway.sh'
if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw 'Gateway native installer is missing' }
$source = Get-Content -LiteralPath $path -Raw
foreach ($token in @('--check', '--apply', '--public-origin', 'aimili-gateway.service', 'aimili-gateway-account', 'GATEWAY_CONFIG', 'aimili-gateway.db', 'mixedSourceCidrs', 'systemd-creds', 'LoadCredentialEncrypted', '/run/credentials/aimili-gateway.service/gateway-master-key')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "Gateway installer contract missing: $token" }
}
if ($source -match 'docker|ssh ny|v2rayN') { throw 'Gateway installer contains an out-of-scope integration' }
if ($source -match 'token_urlsafe\(32\)|["'']publicOrigin["'']\s*:\s*["'']http://127') { throw 'Gateway installer retains insecure key or origin generation' }
Write-Output 'PASS Gateway native installer contract'
