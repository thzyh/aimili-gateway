[CmdletBinding()]
param(
    [string]$AimiliVPNRepository = "D:\CodexProject\Github\aimili-vpngate\.worktrees\main-switch-protocol-modes",
    [switch]$RemotePreflight
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$gatewayRepository = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$aimiliRepository = (Resolve-Path $AimiliVPNRepository).Path
$env:GOCACHE = Join-Path $gatewayRepository ".gocache"

function Invoke-Checked {
    param([scriptblock]$Command)
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "验证命令失败，退出码：$LASTEXITCODE"
    }
}

Push-Location $aimiliRepository
try {
    Invoke-Checked { python -m unittest discover -s tests -v }
    Invoke-Checked { git diff --check }
}
finally { Pop-Location }

Push-Location $gatewayRepository
try {
    Invoke-Checked { go test ./... -count=1 }
    Invoke-Checked { npm --prefix web test -- --run }
    Invoke-Checked { npm --prefix web run build }
    Invoke-Checked { python -m unittest discover -s scripts -p "test_*.py" -v }
    Invoke-Checked { git diff --check }
}
finally { Pop-Location }

if ($RemotePreflight) {
    $remoteVerifier = Join-Path $PSScriptRoot "verify-main-switch-protocol-modes-remote.py"
    Invoke-Checked { scp $remoteVerifier "ny:/tmp/verify-main-switch-protocol-modes-remote.py" }
    Invoke-Checked { ssh ny "python3 /tmp/verify-main-switch-protocol-modes-remote.py preflight" }
}

Write-Host "主连接与独立协议模式本地验证通过。"
