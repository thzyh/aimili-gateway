[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\start-local-vm.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    throw 'local VM startup script is missing'
}

$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($serviceName in @('VMAuthdService', 'VMnetDHCP', 'VMware NAT Service')) {
    if ($source -notmatch [regex]::Escape($serviceName)) {
        throw "startup script omits VMware service: $serviceName"
    }
}
foreach ($required in @('Start-Service', 'repair-host-route.ps1', 'routeParameters.Apply', 'vmrun.exe', "'start'", "'nogui'", 'status.ps1', 'nativeReady', 'ValidateOnly')) {
    if ($source -notmatch [regex]::Escape($required)) {
        throw "startup script contract missing: $required"
    }
}
if ($source -notmatch 'ssh\.exe' -or $source -notmatch "'-b'\s*,\s*\[string\]\`$state\.allowedSource") {
    throw 'startup SSH readiness check does not bind the VMnet8 source address'
}
if ($source -match 'Stop-Process|taskkill|Set-ItemProperty[\s\S]*Internet Settings|Set-DnsClient|Set-NetFirewall|Remove-NetRoute') {
    throw 'startup script can disturb the active proxy or host network'
}

$tokens = $null
$parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path $scriptPath), [ref]$tokens, [ref]$parseErrors) | Out-Null
if ($parseErrors.Count -gt 0) {
    throw ('startup script does not parse: ' + ($parseErrors[0].Message))
}

Write-Output 'PASS local VM startup contract'
