[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force

$lock = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'image-lock.json') -Raw | ConvertFrom-Json
Test-AimiliImageLock -Lock $lock | Out-Null
$paths = Get-AimiliLocalVmPaths
$cacheDirectory = Join-Path $paths.VmRoot 'cache'
$target = Join-Path $cacheDirectory ([string]$lock.fileName)
$partial = "$target.part"

New-Item -ItemType Directory -Path $cacheDirectory -Force | Out-Null
if (Test-Path -LiteralPath $target) {
    if (Confirm-AimiliFileDigest -Path $target -Length ([long]$lock.sizeBytes) -Sha256 ([string]$lock.sha256)) {
        Write-Output $target
        exit 0
    }
    throw 'cached_image_digest_mismatch'
}

if ((Test-Path -LiteralPath $partial) -and (Get-Item -LiteralPath $partial).Length -gt [long]$lock.sizeBytes) {
    Remove-Item -LiteralPath $partial -Force
}

& curl.exe --fail --location --retry 3 --retry-delay 2 --continue-at - --output $partial ([string]$lock.url)
if ($LASTEXITCODE -ne 0) { throw "image_download_failed_exit_$LASTEXITCODE" }
if (-not (Confirm-AimiliFileDigest -Path $partial -Length ([long]$lock.sizeBytes) -Sha256 ([string]$lock.sha256))) {
    Remove-Item -LiteralPath $partial -Force
    throw 'downloaded_image_digest_mismatch'
}

Move-Item -LiteralPath $partial -Destination $target
Write-Output $target
