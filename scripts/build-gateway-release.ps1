[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$')]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$PrivateKey,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$privateKeyPath = [System.IO.Path]::GetFullPath($PrivateKey)
$outputPath = [System.IO.Path]::GetFullPath($OutputDirectory)
$temporaryRoot = Join-Path $projectRoot '.tmp\gateway-release-build'
$binaryPath = Join-Path $temporaryRoot 'aimili-gateway'
$commit = (git -C $projectRoot rev-parse HEAD).Trim()
$builtAt = [DateTimeOffset]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
$schemaVersion = Get-ChildItem (Join-Path $projectRoot 'internal\store\migrations') -File -Filter '*.sql' |
    ForEach-Object { if ($_.BaseName -match '^(\d+)') { [int]$Matches[1] } } |
    Measure-Object -Maximum |
    Select-Object -ExpandProperty Maximum

if (-not (Test-Path -LiteralPath $privateKeyPath -PathType Leaf)) {
    throw "签名私钥不存在：$privateKeyPath"
}
if ((Test-Path -LiteralPath $outputPath) -and (Get-ChildItem -LiteralPath $outputPath -Force | Select-Object -First 1)) {
    throw "输出目录必须为空：$outputPath"
}
if (-not $schemaVersion) {
    throw '无法确定数据库版本。'
}

New-Item -ItemType Directory -Force -Path $temporaryRoot | Out-Null
try {
    Write-Host '正在构建 Aimili Gateway 前端……'
    & npm run build --prefix (Join-Path $projectRoot 'web')
    if ($LASTEXITCODE -ne 0) { throw '前端构建失败。' }

    Write-Host "正在构建 $Version Linux AMD64 程序……"
    $oldGoOS, $oldGoArch, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
    try {
        $env:GOOS = 'linux'
        $env:GOARCH = 'amd64'
        $env:CGO_ENABLED = '0'
        $ldflags = "-s -w -X github.com/thzyh/aimili-gateway/internal/buildinfo.Version=$Version -X github.com/thzyh/aimili-gateway/internal/buildinfo.Commit=$commit -X github.com/thzyh/aimili-gateway/internal/buildinfo.BuiltAt=$builtAt"
        & go build -trimpath -buildvcs=false -ldflags $ldflags -o $binaryPath ./cmd/aimili-gateway
        if ($LASTEXITCODE -ne 0) { throw 'Gateway 构建失败。' }
    }
    finally {
        $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldGoOS, $oldGoArch, $oldCGO
    }

    New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
    & go run ./cmd/aimili-gateway-release-tool gateway `
        --binary $binaryPath `
        --private-key $privateKeyPath `
        --out $outputPath `
        --version $Version `
        --commit $commit `
        --built-at $builtAt `
        --min-database-schema $schemaVersion `
        --max-database-schema $schemaVersion
    if ($LASTEXITCODE -ne 0) { throw '签名发布包生成失败。' }
    Write-Host "发布包已生成：$outputPath"
}
finally {
    if (Test-Path -LiteralPath $binaryPath) {
        Remove-Item -LiteralPath $binaryPath -Force
    }
}
