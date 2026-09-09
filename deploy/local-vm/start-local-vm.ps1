[CmdletBinding()]
param(
    [ValidateRange(30, 900)]
    [int]$SshTimeoutSeconds = 180,

    [ValidateRange(60, 1800)]
    [int]$ReadyTimeoutSeconds = 600,

    [switch]$ValidateOnly,
    [switch]$AsJson
)

$ErrorActionPreference = 'Stop'

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not $ValidateOnly -and -not (Test-Administrator)) {
    $elevatedArguments = @(
        '-NoProfile',
        '-ExecutionPolicy', 'Bypass',
        '-File', ('"{0}"' -f $PSCommandPath),
        '-SshTimeoutSeconds', [string]$SshTimeoutSeconds,
        '-ReadyTimeoutSeconds', [string]$ReadyTimeoutSeconds
    )
    if ($AsJson) { $elevatedArguments += '-AsJson' }
    $elevated = Start-Process -FilePath 'powershell.exe' -Verb RunAs -ArgumentList $elevatedArguments -Wait -PassThru
    if ($elevated.ExitCode -ne 0) {
        throw "local_vm_elevated_start_failed:$($elevated.ExitCode)"
    }

    # The elevated console is separate. Re-read the final state in this console
    # so a manual invocation always receives a durable result.
    $validationParameters = @{
        SshTimeoutSeconds = $SshTimeoutSeconds
        ReadyTimeoutSeconds = $ReadyTimeoutSeconds
        ValidateOnly = $true
        AsJson = [bool]$AsJson
    }
    & $PSCommandPath @validationParameters
    exit $LASTEXITCODE
}

Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force
$paths = Get-AimiliLocalVmPaths
$statePath = Join-Path $paths.RuntimeRoot 'state.json'
$keyPath = Join-Path $paths.RuntimeRoot 'id_ed25519'
$knownHosts = Join-Path $paths.RuntimeRoot 'known_hosts'
$vmrunPath = Join-Path $paths.VmwareRoot 'vmrun.exe'
$repairPath = Join-Path $PSScriptRoot 'repair-host-route.ps1'
$statusPath = Join-Path $PSScriptRoot 'status.ps1'

foreach ($requiredPath in @($statePath, $keyPath, $knownHosts, $vmrunPath, $paths.VmxPath, $repairPath, $statusPath)) {
    if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
        throw "local_vm_startup_input_missing:$requiredPath"
    }
}

$startedAt = [DateTimeOffset]::Now
$serviceStates = [ordered]@{}
foreach ($serviceName in @('VMAuthdService', 'VMnetDHCP', 'VMware NAT Service')) {
    $service = Get-Service -Name $serviceName -ErrorAction Stop
    if ($service.Status -ne [ServiceProcess.ServiceControllerStatus]::Running) {
        if ($ValidateOnly) { throw "vmware_service_not_running:$serviceName" }
        Start-Service -Name $serviceName -ErrorAction Stop
        $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(30))
        $service.Refresh()
    }
    $serviceStates[$serviceName] = [string]$service.Status
}

$routeParameters = @{ AsJson = $true }
if (-not $ValidateOnly) { $routeParameters.Apply = $true }
$routeOutput = @(& $repairPath @routeParameters)
$route = ($routeOutput -join "`n") | ConvertFrom-Json -ErrorAction Stop
if (-not $route.ready -or (-not $ValidateOnly -and -not $route.persistentRoute)) {
    throw 'local_vm_host_route_not_ready'
}

$vmrunOutput = @(& $vmrunPath 'list' 2>&1)
if ($LASTEXITCODE -ne 0) { throw "vmrun_list_failed:$LASTEXITCODE" }
$vmRunning = @($vmrunOutput | Select-Object -Skip 1) -contains $paths.VmxPath
$vmStarted = $false
if (-not $vmRunning) {
    if ($ValidateOnly) { throw 'local_vm_not_running' }
    $startOutput = @(& $vmrunPath 'start' $paths.VmxPath 'nogui' 2>&1)
    if ($LASTEXITCODE -ne 0) { throw "vmrun_start_failed:$LASTEXITCODE" }
    $vmStarted = $true
}

$state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json -ErrorAction Stop
$sshTarget = "aimili@$($state.guestAddress)"
$sshArguments = @(
    '-b', [string]$state.allowedSource,
    '-i', $keyPath,
    '-o', 'BatchMode=yes',
    '-o', 'ConnectTimeout=5',
    '-o', 'StrictHostKeyChecking=yes',
    '-o', "UserKnownHostsFile=$knownHosts"
)
$sshDeadline = (Get-Date).AddSeconds($SshTimeoutSeconds)
$sshReachable = $false
do {
    & ssh.exe @sshArguments $sshTarget 'true' 2>$null
    if ($LASTEXITCODE -eq 0) {
        $sshReachable = $true
        break
    }
    if ($ValidateOnly -or (Get-Date) -ge $sshDeadline) { break }
    Start-Sleep -Seconds 5
} while ($true)
if (-not $sshReachable) { throw 'local_vm_ssh_timeout' }

$readyDeadline = (Get-Date).AddSeconds($ReadyTimeoutSeconds)
$nativeStatus = $null
do {
    $statusOutput = @(& $statusPath -AsJson)
    $nativeStatus = ($statusOutput -join "`n") | ConvertFrom-Json -ErrorAction Stop
    if ($nativeStatus.nativeReady) { break }
    if ($ValidateOnly -or (Get-Date) -ge $readyDeadline) { break }
    Start-Sleep -Seconds 15
} while ($true)
if ($null -eq $nativeStatus -or -not $nativeStatus.nativeReady) {
    throw 'local_vm_native_ready_timeout'
}

$report = [pscustomobject][ordered]@{
    ready = $true
    validateOnly = [bool]$ValidateOnly
    vmStarted = $vmStarted
    vmRunning = $true
    vmxPath = $paths.VmxPath
    services = [pscustomobject]$serviceStates
    hostRoute = [pscustomobject][ordered]@{
        ready = [bool]$route.ready
        persistent = [bool]$route.persistentRoute
        selectedInterface = [string]$route.selectedInterface
        guestAddress = [string]$route.guestAddress
        sourceAddress = [string]$route.sourceAddress
    }
    sshReachable = $sshReachable
    nativeReady = [bool]$nativeStatus.nativeReady
    actual = $nativeStatus.actual
    gatewayUrl = "https://$($state.guestAddress):8080"
    elapsedSeconds = [math]::Round(([DateTimeOffset]::Now - $startedAt).TotalSeconds, 1)
}

if ($AsJson) { $report | ConvertTo-Json -Depth 6 -Compress } else { $report }
