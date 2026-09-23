param(
    [string]$Tag = 'v0.2.0-vps',
    [string]$XUISource = 'D:\CodexProject\Github\.tmp\3x-ui-v3.7.0',
    [string]$SigningKey = 'C:\Users\zyh\.codex\keys\aimili-vps-release-ed25519.pem',
    [string]$Output = 'D:\CodexProject\Github\.tmp\aimili-vps-release'
)
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$source = (Resolve-Path $XUISource).Path
$patch = Join-Path $PSScriptRoot '3x-ui-v3.7.0-alias.patch'
$expected = 'f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
if ((git -C $source rev-parse HEAD).Trim() -ne $expected) { throw '3x-ui upstream commit mismatch' }
if (-not (Test-Path (Join-Path $source 'internal/web/dist/index.html'))) { throw '3x-ui frontend dist missing' }
if (-not (Test-Path $SigningKey)) { throw 'release signing key missing' }
New-Item -ItemType Directory -Force $Output | Out-Null
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
try {
    Push-Location $root
    try {
        npm run build --prefix web
        if ($LASTEXITCODE -ne 0) { throw 'Gateway frontend build failed' }
        New-Item -ItemType Directory -Force (Join-Path $Output 'package\bin') | Out-Null
        go build -trimpath -buildvcs=false -o (Join-Path $Output 'package\bin\aimili-gateway') ./cmd/aimili-gateway
        if ($LASTEXITCODE -ne 0) { throw 'Gateway Linux build failed' }
        go build -trimpath -buildvcs=false -o (Join-Path $Output 'package\bin\aimili-gateway-admin') ./cmd/aimili-gateway-admin
        if ($LASTEXITCODE -ne 0) { throw 'Gateway admin Linux build failed' }
    } finally { Pop-Location }
} finally {
    Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED -ErrorAction SilentlyContinue
}
foreach ($part in @('deploy\vps','deploy\systemd','deploy\bin','deploy\local-vm\native','scripts','services\aimili-egress')) {
    $destination = Join-Path $Output (Join-Path 'package' $part)
    New-Item -ItemType Directory -Force $destination | Out-Null
}
Copy-Item -LiteralPath (Join-Path $root 'deploy\vps\installer.py'),(Join-Path $root 'deploy\vps\provision.py') -Destination (Join-Path $Output 'package\deploy\vps')
Copy-Item -LiteralPath (Join-Path $root 'deploy\systemd\aimilivpn.service'),(Join-Path $root 'deploy\systemd\aimili-gateway.service'),(Join-Path $root 'deploy\systemd\aimili-xui-protocol-transaction.path'),(Join-Path $root 'deploy\systemd\aimili-xui-protocol-transaction.timer'),(Join-Path $root 'deploy\systemd\aimili-xui-protocol-transaction.service') -Destination (Join-Path $Output 'package\deploy\systemd')
Copy-Item -LiteralPath (Join-Path $root 'deploy\bin\aimili-gateway-account'),(Join-Path $root 'deploy\bin\aimili-xui-protocol-transaction') -Destination (Join-Path $Output 'package\deploy\bin')
Copy-Item -LiteralPath (Join-Path $root 'deploy\local-vm\native\x-ui.service.debian'),(Join-Path $root 'deploy\local-vm\native\rotate-xui-account.py') -Destination (Join-Path $Output 'package\deploy\local-vm\native')
Copy-Item -LiteralPath (Join-Path $root 'scripts\aimili_xui_protocol_transaction.py') -Destination (Join-Path $Output 'package\scripts')
Get-ChildItem (Join-Path $root 'services\aimili-egress') -Filter '*.py' -File | Copy-Item -Destination (Join-Path $Output 'package\services\aimili-egress')
tar -czf (Join-Path $Output 'aimili-vps-package.tar.gz') -C (Join-Path $Output 'package') .
if ($LASTEXITCODE -ne 0) { throw 'package archive failed' }
$xui = Join-Path $source 'x-ui-custom'
if (-not (Test-Path $xui)) { throw 'custom 3x-ui binary missing' }
Copy-Item -LiteralPath $xui -Destination (Join-Path $Output 'x-ui-custom-linux-amd64')
$manifest = [ordered]@{ schemaVersion=1; release=$Tag; gatewayCommit=(git -C $root rev-parse HEAD).Trim(); xuiUpstreamCommit=$expected; assets=[ordered]@{package=[ordered]@{name='aimili-vps-package.tar.gz';sha256=(Get-FileHash (Join-Path $Output 'aimili-vps-package.tar.gz') -Algorithm SHA256).Hash.ToLower()};xui=[ordered]@{name='x-ui-custom-linux-amd64';sha256=(Get-FileHash (Join-Path $Output 'x-ui-custom-linux-amd64') -Algorithm SHA256).Hash.ToLower()}}}
[System.IO.File]::WriteAllText((Join-Path $Output 'manifest.json'),(($manifest | ConvertTo-Json -Depth 8 -Compress)+"`n"),(New-Object System.Text.UTF8Encoding($false)))
& 'D:\SoftWare\Git\mingw64\bin\openssl.exe' pkeyutl -sign -rawin -inkey $SigningKey -in (Join-Path $Output 'manifest.json') -out (Join-Path $Output 'manifest.sig')
if ($LASTEXITCODE -ne 0) { throw 'manifest signing failed' }
Write-Host "发布资产已生成：$Output"
