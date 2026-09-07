[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\deploy-native.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) { throw 'native deployment entry is missing' }
$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($token in @('PlanOnly', 'ResumeFrom', 'state.json', 'StrictHostKeyChecking=yes', 'stage.sh', 'backup.sh', 'rollback.sh', 'install-aimilivpn.sh', 'install-xui-caddy.sh', 'install-gateway.sh', 'enable-exits.sh', 'verify-native.sh')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "native deployment entry missing contract: $token" }
}
$statePath = Join-Path ([IO.Path]::GetTempPath()) ("aimili-state-{0}.json" -f [guid]::NewGuid().ToString('N'))
try {
    @{ guestAddress = '192.168.88.4'; allowedSource = '192.168.88.1' } | ConvertTo-Json | Set-Content -LiteralPath $statePath -Encoding utf8NoBOM
    $output = & $scriptPath -PlanOnly -StatePath $statePath
    if ($LASTEXITCODE -ne 0) { throw "native PlanOnly failed with exit code $LASTEXITCODE" }
    $plan = $output | ConvertFrom-Json
    if ($plan.mode -ne 'plan') { throw 'native PlanOnly performed apply' }
    if ($plan.guestAddress -ne '192.168.88.4' -or $plan.allowedSource -ne '192.168.88.1') { throw 'native PlanOnly state mismatch' }
    if ((@($plan.stages) -join ',') -ne 'aimilivpn,xui-caddy,gateway,slots,verify') { throw 'native deployment stage order changed' }
} finally {
    if (Test-Path -LiteralPath $statePath) { Remove-Item -LiteralPath $statePath -Force }
}
Write-Output 'PASS native deployment entry contract'
