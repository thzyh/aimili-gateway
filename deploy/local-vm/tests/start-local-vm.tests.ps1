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
foreach ($required in @('Start-Service', 'repair-host-route.ps1', 'Apply = $true', 'vmrun.exe', "'start'", "'nogui'", 'status.ps1', 'nativeReady', 'ValidateOnly', 'Read-Host', 'startup-last-result.json', 'ResultPath')) {
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

$startInfo = [Diagnostics.ProcessStartInfo]::new()
$startInfo.FileName = 'powershell.exe'
$startInfo.Arguments = '-NoProfile -ExecutionPolicy Bypass -File "{0}"' -f $scriptPath
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$process = [Diagnostics.Process]::Start($startInfo)
try {
    $process.StandardInput.WriteLine('0')
    $process.StandardInput.Close()
    if (-not $process.WaitForExit(10000)) {
        $process.Kill()
        throw 'startup menu did not accept the exit selection'
    }
    $menuOutput = $process.StandardOutput.ReadToEnd()
    $menuError = $process.StandardError.ReadToEnd()
    if ($process.ExitCode -ne 0 -or -not [string]::IsNullOrWhiteSpace($menuError)) {
        throw 'startup menu smoke test failed'
    }
    if ($menuOutput -notmatch 'AimiliGatewayLocal startup manager' -or $menuOutput -notmatch 'Start services') {
        throw 'startup menu options were not rendered'
    }
} finally {
    if (-not $process.HasExited) { $process.Kill() }
    $process.Dispose()
}

Write-Output 'PASS local VM startup contract'
