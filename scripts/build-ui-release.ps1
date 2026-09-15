param(
    [Parameter(Mandatory = $true)][string]$SigningKey,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$keyPath = [System.IO.Path]::GetFullPath($SigningKey)
$outputPath = [System.IO.Path]::GetFullPath($OutputDirectory)

npm --prefix (Join-Path $projectRoot 'web') test -- --run
if ($LASTEXITCODE -ne 0) { throw 'Gateway 前端测试失败。' }
npm --prefix (Join-Path $projectRoot 'web') run build
if ($LASTEXITCODE -ne 0) { throw 'Gateway 前端生产构建失败。' }

$commit = (git -C $projectRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or -not $commit) { throw '无法读取当前 Git 提交。' }
$builtAt = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')

Push-Location $projectRoot
try {
    go run ./cmd/aimili-gateway-release-tool ui `
        --dist (Join-Path $projectRoot 'internal/webassets/dist') `
        --private-key $keyPath `
        --out $outputPath `
        --commit $commit `
        --built-at $builtAt
    if ($LASTEXITCODE -ne 0) { throw 'Gateway UI Release 构建失败。' }
}
finally {
    Pop-Location
}
