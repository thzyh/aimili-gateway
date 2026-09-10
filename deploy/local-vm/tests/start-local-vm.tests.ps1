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
foreach ($required in @('Start-Service', 'Stop-Service', 'repair-host-route.ps1', 'Apply = $true', 'vmrun.exe', "'start'", "'nogui'", "'stop'", "'soft'", 'status.ps1', 'nativeReady', 'ValidateOnly', 'Read-Host', 'startup-last-result.json', 'ResultPath', 'vmware-tray.exe', 'otherRunningVms', 'Test-SshReadiness', 'Write-Progress', '管理员任务仍在运行')) {
    if ($source -notmatch [regex]::Escape($required)) {
        throw "startup script contract missing: $required"
    }
}
if ($source -notmatch 'ssh\.exe' -or $source -notmatch "'-b'\s*,\s*\[string\]\`$state\.allowedSource") {
    throw 'startup SSH readiness check does not bind the VMnet8 source address'
}
if ($source.IndexOf("if (`$Mode -eq 'Start') { `$trayRunning = Start-VMwareTray") -gt $source.IndexOf("`$script:CurrentStage = 'ssh-readiness'")) {
    throw 'VMware tray startup must not wait for SSH readiness'
}
if ($source -notmatch "Test-SshReadiness[\s\S]*?ErrorActionPreference = 'Continue'[\s\S]*?LASTEXITCODE -eq 0") {
    throw 'SSH readiness probe can still terminate instead of retrying transient startup errors'
}
if ($source -match 'taskkill|Set-ItemProperty[\s\S]*Internet Settings|Set-DnsClient|Set-NetFirewall|Remove-NetRoute') {
    throw 'startup script can disturb the active proxy or host network'
}
if ($source -notmatch '停止 AimiliGatewayLocal' -or $source -notmatch '启动或恢复 AimiliGatewayLocal') {
    throw 'startup menu prompts are not Chinese'
}

$tokens = $null
$parseErrors = $null
$scriptAst = [System.Management.Automation.Language.Parser]::ParseFile((Resolve-Path $scriptPath), [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count -gt 0) {
    throw ('startup script does not parse: ' + ($parseErrors[0].Message))
}

$sshProbeAst = $scriptAst.Find({
    param($node)
    $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Test-SshReadiness'
}, $true)
if ($null -eq $sshProbeAst) { throw 'SSH readiness function was not found in the parsed script' }
. ([scriptblock]::Create($sshProbeAst.Extent.Text))
$previousPreference = $ErrorActionPreference
try {
    $ErrorActionPreference = 'Stop'
    $unreachable = Test-SshReadiness -Arguments @('-o', 'BatchMode=yes', '-o', 'ConnectTimeout=1') -Target '192.0.2.1'
    if ($unreachable) { throw 'unreachable SSH fixture was unexpectedly reachable' }
} finally {
    $ErrorActionPreference = $previousPreference
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
    if ($menuOutput -notmatch 'AimiliGatewayLocal 启动管理器' -or $menuOutput -notmatch '启动或恢复 AimiliGatewayLocal') {
        throw 'startup menu options were not rendered'
    }
} finally {
    if (-not $process.HasExited) { $process.Kill() }
    $process.Dispose()
}

Write-Output 'PASS local VM startup contract'
