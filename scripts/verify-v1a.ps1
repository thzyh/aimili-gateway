[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$env:GOTOOLCHAIN = 'local'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$buildDirectory = Join-Path ([IO.Path]::GetTempPath()) ('aimili-gateway-verify-' + [IO.Path]::GetRandomFileName())
$gitExecutable = (Get-Command git -ErrorAction Stop).Source
$gitRoot = Split-Path -Parent (Split-Path -Parent $gitExecutable)
$gitBash = Join-Path $gitRoot 'bin\bash.exe'
if (-not (Test-Path -LiteralPath $gitBash)) {
    $gitBash = (Get-Command bash -ErrorAction Stop).Source
}

function Invoke-NativeStep {
    param(
        [Parameter(Mandatory)] [string] $Name,
        [Parameter(Mandatory)] [scriptblock] $Command
    )
    Write-Host "[verify] $Name"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
}

function Assert-NoTrackedMatch {
    param(
        [Parameter(Mandatory)] [string] $Category,
        [Parameter(Mandatory)] [string] $Pattern
    )
    $matches = @(& git grep -Il -i -E -- $Pattern 2>$null)
    $exitCode = $LASTEXITCODE
    if ($exitCode -gt 1) {
        throw "secret scan failed for category $Category"
    }
    if ($matches.Count -gt 0) {
        throw "$Category detected in tracked files: $($matches -join ', ')"
    }
}

Push-Location $repositoryRoot
try {
    New-Item -ItemType Directory -Path $buildDirectory | Out-Null

    Invoke-NativeStep 'Vue tests' { npm test --prefix web }
    Invoke-NativeStep 'Vue production build' { npm run build --prefix web }
    Invoke-NativeStep 'Go race tests' { go test ./... -race -count=1 }
    Invoke-NativeStep 'Go vet' { go vet ./... }
    Invoke-NativeStep 'Gateway build' { go build -o (Join-Path $buildDirectory 'aimili-gateway.exe') ./cmd/aimili-gateway }
    Invoke-NativeStep 'Admin CLI build' { go build -o (Join-Path $buildDirectory 'aimili-gateway-admin.exe') ./cmd/aimili-gateway-admin }
    Invoke-NativeStep 'Account command syntax' { & $gitBash -n deploy/bin/aimili-gateway-account }
    Invoke-NativeStep 'Git whitespace check' { git diff --check }

    Write-Host '[verify] Tracked artifact scan'
    $trackedFiles = @(& git ls-files)
    if ($LASTEXITCODE -ne 0) {
        throw 'unable to list tracked files'
    }
    $forbiddenFiles = @($trackedFiles | Where-Object {
        $_ -match '(?i)(^|/)[.]env(?:[.]|$)' -or
        $_ -match '(?i)[.](?:db|db-shm|db-wal|credential|credentials)$' -or
        $_ -match '(?i)(^|/)(?:node_modules|dist)(/|$)'
    })
    if ($forbiddenFiles.Count -gt 0) {
        throw "generated or credential artifacts are tracked: $($forbiddenFiles -join ', ')"
    }

    Assert-NoTrackedMatch 'private-key material' '-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----'
    Assert-NoTrackedMatch 'complete proxy connection URI' '(vless|vmess|trojan|ss)://[[:alnum:]]'
    Assert-NoTrackedMatch 'UUID-shaped literal' '[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}'
    Assert-NoTrackedMatch 'literal HTTP credential header' '(cookie|set-cookie|authorization):[[:space:]]*[A-Za-z0-9_-]{16,}'

    Write-Host '[verify] V1-A verification passed'
}
finally {
    Pop-Location
    if (Test-Path -LiteralPath $buildDirectory) {
        Remove-Item -LiteralPath $buildDirectory -Recurse -Force
    }
}
