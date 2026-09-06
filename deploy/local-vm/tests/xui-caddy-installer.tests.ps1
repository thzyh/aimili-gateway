[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\native\install-xui-caddy.sh'
$caddyPath = Join-Path $PSScriptRoot '..\native\local-caddy.Caddyfile'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) { throw 'x-ui/Caddy installer is missing' }
if (-not (Test-Path -LiteralPath $caddyPath -PathType Leaf)) { throw 'local Caddyfile is missing' }
$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($token in @('--check', '--apply', '--allowed-source', 'x-ui.service', 'caddy.service', 'v3.7.0', '127.0.0.1', 'ufw allow from')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "x-ui/Caddy installer contract missing: $token" }
}
$caddy = Get-Content -LiteralPath $caddyPath -Raw
if ($caddy -notmatch ':8080') { throw 'local Caddyfile does not use the local HTTP listener' }
if ($caddy -match 'https://|tls\s') { throw 'local Caddyfile unexpectedly enables public TLS' }
if ($source -match 'docker|ssh ny|v2rayN') { throw 'x-ui/Caddy installer contains an out-of-scope integration' }
Write-Output 'PASS x-ui/Caddy native installer contract'
