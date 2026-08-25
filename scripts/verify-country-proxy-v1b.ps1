[CmdletBinding()]
param(
    [string]$AimiliVPNRepository = (Join-Path $PSScriptRoot "..\..\aimili-vpngate")
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$gatewayRepository = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$aimiliRepository = (Resolve-Path $AimiliVPNRepository).Path
$env:GOCACHE = Join-Path $env:TEMP "aimili-gateway-gocache"

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
	$gatewayBinary = Join-Path $env:TEMP "aimili-gateway-verify.exe"
	$adminBinary = Join-Path $env:TEMP "aimili-gateway-admin-verify.exe"
	Invoke-Checked { go test ./... }
	Invoke-Checked { go vet ./... }
	Invoke-Checked { go build -o $gatewayBinary ./cmd/aimili-gateway }
	Invoke-Checked { go build -o $adminBinary ./cmd/aimili-gateway-admin }
	Invoke-Checked { git diff --check }
	Remove-Item -LiteralPath $gatewayBinary, $adminBinary -Force -ErrorAction SilentlyContinue
}
finally {
    Pop-Location
}

Write-Host "V1-B 本地合同、前端构建和双仓库测试均通过。"
