[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\native\install-xui-caddy.sh'
$caddyPath = Join-Path $PSScriptRoot '..\native\local-caddy.Caddyfile'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) { throw 'x-ui/Caddy installer is missing' }
if (-not (Test-Path -LiteralPath $caddyPath -PathType Leaf)) { throw 'local Caddyfile is missing' }
$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($token in @('--check', '--apply', '--allowed-source', '--public-origin', 'x-ui.service', 'caddy.service', 'v3.7.0', 'f727d04f6522bb94a8fb52e8352fdcafb51c11e1', '0f8dd7baef3458f6591574e24814f322cf7f5e1e27f0a594683745e50be84ec5', '127.0.0.1', 'ufw allow from', 'xui-credentials.json', 'update-ca-certificates', 'aimili-local-caddy.crt', 'openssl verify')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "x-ui/Caddy installer contract missing: $token" }
}
$caddy = Get-Content -LiteralPath $caddyPath -Raw
if ($caddy -notmatch ':8080') { throw 'local Caddyfile does not use the local HTTP listener' }
if ($caddy -notmatch 'tls internal|/vpngate/|127\.0\.0\.1:8787') { throw 'local Caddyfile is missing native HTTPS routes' }
if ($source -match 'docker|ssh ny|v2rayN') { throw 'x-ui/Caddy installer contains an out-of-scope integration' }
Write-Output 'PASS x-ui/Caddy native installer contract'
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..')
Push-Location $repoRoot
try {
    & wsl.exe -u root -- bash 'deploy/local-vm/tests/native-installers-fixture.tests.sh'
    if ($LASTEXITCODE -ne 0) { throw "native installer fixture failed with exit code $LASTEXITCODE" }
} finally { Pop-Location }
