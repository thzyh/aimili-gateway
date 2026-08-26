[CmdletBinding()]
param(
    [string]$AimiliVPNRepository = (Join-Path $PSScriptRoot "..\..\aimili-vpngate"),
    [string]$DeployRepository = (Join-Path $PSScriptRoot "..\..\aimili-3xui-simple-deploy")
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$gatewayRepository = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$aimiliRepository = (Resolve-Path $AimiliVPNRepository).Path
$deployRepository = (Resolve-Path $DeployRepository).Path
$env:GOCACHE = Join-Path $env:TEMP "aimili-gateway-v1c-gocache"
$env:GOFLAGS = (($env:GOFLAGS + " -buildvcs=false").Trim())

function Invoke-Checked {
    param([scriptblock]$Command)
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "验证命令失败，退出码：$LASTEXITCODE"
    }
}

Push-Location $aimiliRepository
try {
    Invoke-Checked { python -m compileall -q control_api.py vpngate_manager.py tests }
    Invoke-Checked { python -m unittest discover -s tests -v }
    Invoke-Checked { git -c "safe.directory=$aimiliRepository" diff --check }
}
finally {
    Pop-Location
}

Push-Location (Join-Path $gatewayRepository "web")
try {
    Invoke-Checked { npm test -- --run }
    Invoke-Checked { npm run build }
}
finally {
    Pop-Location
}

Push-Location $gatewayRepository
try {
    $gatewayBinary = Join-Path $env:TEMP "aimili-gateway-v1c-verify.exe"
    $adminBinary = Join-Path $env:TEMP "aimili-gateway-admin-v1c-verify.exe"
    Invoke-Checked { go test ./... -count=1 }
    Invoke-Checked { go test ./... -race -count=1 }
    Invoke-Checked { go vet ./... }
    Invoke-Checked { go build -o $gatewayBinary ./cmd/aimili-gateway }
    Invoke-Checked { go build -o $adminBinary ./cmd/aimili-gateway-admin }
    Invoke-Checked { git -c "safe.directory=$gatewayRepository" diff --check }
    Remove-Item -LiteralPath $gatewayBinary, $adminBinary -Force -ErrorAction SilentlyContinue
}
finally {
    Pop-Location
}

Push-Location $deployRepository
try {
    Invoke-Checked { pwsh -NoProfile -File .\tests\run.ps1 }
}
finally {
    Pop-Location
}

Write-Host "V1-C 双仓库测试、前端构建、Go race/vet/build 和部署契约均通过。"
