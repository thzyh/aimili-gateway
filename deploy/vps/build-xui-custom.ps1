param([string]$Output = 'D:\CodexProject\Github\.tmp\aimili-vps-release\x-ui-custom-linux-amd64')
$ErrorActionPreference = 'Stop'
$commit = 'f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
$tempRoot = [System.IO.Path]::GetFullPath($env:TEMP)
$root = [System.IO.Path]::GetFullPath((Join-Path $tempRoot ('aimili-xui-' + [guid]::NewGuid().ToString('N'))))
if (-not $root.StartsWith($tempRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) { throw 'temporary path escapes expected directory' }
try {
    git clone --quiet --depth 1 --branch v3.7.0 https://github.com/MHSanaei/3x-ui.git $root
    if ($LASTEXITCODE -ne 0 -or (git -C $root rev-parse HEAD).Trim() -ne $commit) { throw '3x-ui pinned source mismatch' }
    git -C $root apply (Join-Path $PSScriptRoot '3x-ui-v3.7.0-alias.patch')
    if ($LASTEXITCODE -ne 0) { throw '3x-ui alias patch failed' }
    docker run --rm -v "${root}:/src" -w /src/frontend node:24-alpine sh -c 'npm ci --ignore-scripts && npm run build'
    if ($LASTEXITCODE -ne 0) { throw '3x-ui frontend build failed' }
    docker run --rm -v "${root}:/src" -w /src golang:1.27.0-bookworm bash -c '/usr/local/go/bin/go test ./internal/database ./internal/web/service ./internal/web/controller ./internal/sub -run "Alias|ClientInbound" -count=1 && /usr/local/go/bin/go build -o /src/x-ui-custom ./'
    if ($LASTEXITCODE -ne 0) { throw '3x-ui backend build failed' }
    New-Item -ItemType Directory -Force (Split-Path $Output -Parent) | Out-Null
    Copy-Item (Join-Path $root 'x-ui-custom') $Output
    Write-Host "3x-ui 已构建：$Output"
} finally {
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
}
