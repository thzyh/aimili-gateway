[CmdletBinding()]
param(
    [ValidateSet('Menu', 'Start', 'Status', 'RepairRoute')]
    [string]$Action = 'Menu',
    [ValidateRange(30, 900)]
    [int]$SshTimeoutSeconds = 180,
    [ValidateRange(60, 1800)]
    [int]$ReadyTimeoutSeconds = 600,
    [switch]$ValidateOnly,
    [switch]$AsJson,
    [string]$ResultPath
)

$ErrorActionPreference = 'Stop'
$script:CurrentStage = 'initialize'

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Write-ResultFile {
    param([Parameter(Mandatory)]$Result, [string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return }
    $parent = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    $temporaryPath = "$Path.part"
    $Result | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $temporaryPath -Encoding utf8
    Move-Item -LiteralPath $temporaryPath -Destination $Path -Force
}

function Get-LocalVmInputs {
    Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force
    $paths = Get-AimiliLocalVmPaths
    $inputs = [pscustomobject]@{
        Paths = $paths
        StatePath = Join-Path $paths.RuntimeRoot 'state.json'
        KeyPath = Join-Path $paths.RuntimeRoot 'id_ed25519'
        KnownHosts = Join-Path $paths.RuntimeRoot 'known_hosts'
        VmrunPath = Join-Path $paths.VmwareRoot 'vmrun.exe'
        RepairPath = Join-Path $PSScriptRoot 'repair-host-route.ps1'
        StatusPath = Join-Path $PSScriptRoot 'status.ps1'
    }
    foreach ($requiredPath in @($inputs.StatePath, $inputs.KeyPath, $inputs.KnownHosts, $inputs.VmrunPath, $paths.VmxPath, $inputs.RepairPath, $inputs.StatusPath)) {
        if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
            throw "local_vm_startup_input_missing:$requiredPath"
        }
    }
    return $inputs
}

function Invoke-RouteRepair {
    param([Parameter(Mandatory)]$Inputs, [bool]$Apply)
    $script:CurrentStage = 'host-route'
    $parameters = @{ AsJson = $true }
    if ($Apply) { $parameters.Apply = $true }
    $output = @(& $Inputs.RepairPath @parameters)
    $route = ($output -join "`n") | ConvertFrom-Json -ErrorAction Stop
    if (-not $route.ready -or ($Apply -and -not $route.persistentRoute)) {
        throw 'local_vm_host_route_not_ready'
    }
    return $route
}

function Invoke-LocalVmOperation {
    param([ValidateSet('Start', 'Status', 'RepairRoute')][string]$Mode)
    $startedAt = [DateTimeOffset]::Now
    $inputs = Get-LocalVmInputs
    $paths = $inputs.Paths

    if ($Mode -eq 'RepairRoute') {
        $route = Invoke-RouteRepair -Inputs $inputs -Apply $true
        return [pscustomobject][ordered]@{
            success = $true; action = $Mode; stage = 'complete'
            hostRoute = [pscustomobject][ordered]@{
                ready = [bool]$route.ready; persistent = [bool]$route.persistentRoute
                selectedInterface = [string]$route.selectedInterface
                guestAddress = [string]$route.guestAddress; sourceAddress = [string]$route.sourceAddress
            }
        }
    }

    $script:CurrentStage = 'vmware-services'
    $serviceStates = [ordered]@{}
    foreach ($serviceName in @('VMAuthdService', 'VMnetDHCP', 'VMware NAT Service')) {
        $service = Get-Service -Name $serviceName -ErrorAction Stop
        if ($service.Status -ne [ServiceProcess.ServiceControllerStatus]::Running) {
            if ($Mode -eq 'Status') { throw "vmware_service_not_running:$serviceName" }
            Start-Service -Name $serviceName -ErrorAction Stop
            $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(30))
            $service.Refresh()
        }
        $serviceStates[$serviceName] = [string]$service.Status
    }

    $route = Invoke-RouteRepair -Inputs $inputs -Apply ($Mode -eq 'Start')
    $script:CurrentStage = 'vmware-runtime'
    $vmrunOutput = @(& $inputs.VmrunPath 'list' 2>&1)
    if ($LASTEXITCODE -ne 0) { throw "vmrun_list_failed:$LASTEXITCODE" }
    $vmRunning = @($vmrunOutput | Select-Object -Skip 1) -contains $paths.VmxPath
    $vmStarted = $false
    if (-not $vmRunning) {
        if ($Mode -eq 'Status') { throw 'local_vm_not_running' }
        $startOutput = @(& $inputs.VmrunPath 'start' $paths.VmxPath 'nogui' 2>&1)
        if ($LASTEXITCODE -ne 0) { throw "vmrun_start_failed:$LASTEXITCODE" }
        $vmStarted = $true
    }

    $state = Get-Content -LiteralPath $inputs.StatePath -Raw | ConvertFrom-Json -ErrorAction Stop
    $sshTarget = "aimili@$($state.guestAddress)"
    $sshArguments = @(
        '-b', [string]$state.allowedSource, '-i', $inputs.KeyPath,
        '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5', '-o', 'StrictHostKeyChecking=yes',
        '-o', "UserKnownHostsFile=$($inputs.KnownHosts)"
    )

    $script:CurrentStage = 'ssh-readiness'
    $sshDeadline = (Get-Date).AddSeconds($SshTimeoutSeconds)
    $sshReachable = $false
    do {
        & ssh.exe @sshArguments $sshTarget 'true' 2>$null
        if ($LASTEXITCODE -eq 0) { $sshReachable = $true; break }
        if ($Mode -eq 'Status' -or (Get-Date) -ge $sshDeadline) { break }
        Start-Sleep -Seconds 5
    } while ($true)
    if (-not $sshReachable) { throw 'local_vm_ssh_timeout' }

    $script:CurrentStage = 'native-readiness'
    $readyDeadline = (Get-Date).AddSeconds($ReadyTimeoutSeconds)
    $nativeStatus = $null
    do {
        $statusOutput = @(& $inputs.StatusPath -AsJson)
        $nativeStatus = ($statusOutput -join "`n") | ConvertFrom-Json -ErrorAction Stop
        if ($nativeStatus.nativeReady) { break }
        if ($Mode -eq 'Status' -or (Get-Date) -ge $readyDeadline) { break }
        Start-Sleep -Seconds 15
    } while ($true)
    if ($null -eq $nativeStatus -or -not $nativeStatus.nativeReady) { throw 'local_vm_native_ready_timeout' }

    $script:CurrentStage = 'complete'
    return [pscustomobject][ordered]@{
        success = $true; action = $Mode; stage = 'complete'; vmStarted = $vmStarted; vmRunning = $true
        vmxPath = $paths.VmxPath; services = [pscustomobject]$serviceStates
        hostRoute = [pscustomobject][ordered]@{
            ready = [bool]$route.ready; persistent = [bool]$route.persistentRoute
            selectedInterface = [string]$route.selectedInterface
            guestAddress = [string]$route.guestAddress; sourceAddress = [string]$route.sourceAddress
        }
        sshReachable = $sshReachable; nativeReady = [bool]$nativeStatus.nativeReady
        actual = $nativeStatus.actual; gatewayUrl = "https://$($state.guestAddress):8080"
        elapsedSeconds = [math]::Round(([DateTimeOffset]::Now - $startedAt).TotalSeconds, 1)
    }
}

function New-FailureResult {
    param([Parameter(Mandatory)]$ErrorRecord, [string]$RequestedAction)
    return [pscustomobject][ordered]@{
        success = $false; action = $RequestedAction; stage = $script:CurrentStage
        error = [string]$ErrorRecord.Exception.Message
        diagnostic = [string]$ErrorRecord.InvocationInfo.PositionMessage
        capturedAt = [DateTimeOffset]::Now.ToString('o')
    }
}

function Invoke-ElevatedOperation {
    param([ValidateSet('Start', 'RepairRoute')][string]$Mode)
    $runtimeRoot = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AimiliGateway\vmware-local'
    $diagnosticPath = Join-Path $runtimeRoot 'startup-last-result.json'
    if (Test-Path -LiteralPath $diagnosticPath) { Remove-Item -LiteralPath $diagnosticPath -Force }
    $arguments = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ('"{0}"' -f $PSCommandPath),
        '-Action', $Mode, '-SshTimeoutSeconds', [string]$SshTimeoutSeconds,
        '-ReadyTimeoutSeconds', [string]$ReadyTimeoutSeconds,
        '-ResultPath', ('"{0}"' -f $diagnosticPath), '-AsJson'
    )
    try {
        $process = Start-Process -FilePath 'powershell.exe' -Verb RunAs -WindowStyle Hidden -ArgumentList $arguments -Wait -PassThru
    } catch {
        throw "administrator_elevation_failed:$($_.Exception.Message)"
    }
    if (-not (Test-Path -LiteralPath $diagnosticPath -PathType Leaf)) {
        throw "elevated_result_missing:exit=$($process.ExitCode)"
    }
    $result = Get-Content -LiteralPath $diagnosticPath -Raw | ConvertFrom-Json -ErrorAction Stop
    if ($process.ExitCode -ne 0 -or -not $result.success) {
        throw "elevated_$($result.stage)_failed:$($result.error); diagnostic=$diagnosticPath"
    }
    return $result
}

function Invoke-RequestedAction {
    param([ValidateSet('Start', 'Status', 'RepairRoute')][string]$Mode)
    if ($Mode -in @('Start', 'RepairRoute') -and -not (Test-Administrator)) {
        return Invoke-ElevatedOperation -Mode $Mode
    }
    return Invoke-LocalVmOperation -Mode $Mode
}

function Show-Result {
    param([Parameter(Mandatory)]$Result)
    if (-not $Result.success) {
        Write-Host "FAILED [$($Result.stage)] $($Result.error)" -ForegroundColor Red
        if ($Result.diagnostic) { Write-Host $Result.diagnostic -ForegroundColor DarkGray }
        return
    }
    if ($Result.action -eq 'RepairRoute') {
        Write-Host 'Host route is ready.' -ForegroundColor Green
        Write-Host "Guest: $($Result.hostRoute.guestAddress) via $($Result.hostRoute.selectedInterface)"
        Write-Host "Persistent: $($Result.hostRoute.persistent)"
        return
    }
    Write-Host 'AimiliGatewayLocal is ready.' -ForegroundColor Green
    Write-Host "VM started by this run: $($Result.vmStarted)"
    Write-Host "SSH reachable: $($Result.sshReachable)"
    Write-Host "Native ready: $($Result.nativeReady)"
    Write-Host "OpenVPN/Xray/logical exits: $($Result.actual.openvpn)/$($Result.actual.xray)/$($Result.actual.logicalExits)"
    Write-Host "Gateway: $($Result.gatewayUrl)"
}

if ($ValidateOnly) { $Action = 'Status' }

if ($Action -eq 'Menu') {
    do {
        Clear-Host
        Write-Host 'AimiliGatewayLocal startup manager' -ForegroundColor Cyan
        Write-Host ''
        Write-Host '1. Start services, repair route, start VM, and wait until ready'
        Write-Host '2. Read-only status check'
        Write-Host '3. Repair or verify the persistent VMnet8 host route'
        Write-Host '0. Exit'
        Write-Host ''
        $choice = Read-Host 'Select'
        if ($choice -eq '0') { break }
        $selected = switch ($choice) {
            '1' { 'Start' }; '2' { 'Status' }; '3' { 'RepairRoute' }; default { $null }
        }
        if ($null -eq $selected) {
            Write-Host 'Invalid selection.' -ForegroundColor Yellow
        } else {
            try {
                Write-Host 'Running, please wait...' -ForegroundColor Cyan
                Show-Result -Result (Invoke-RequestedAction -Mode $selected)
            } catch {
                Show-Result -Result (New-FailureResult -ErrorRecord $_ -RequestedAction $selected)
            }
        }
        Write-Host ''
        [void](Read-Host 'Press Enter to return to the menu')
    } while ($true)
    exit 0
}

try {
    $result = Invoke-RequestedAction -Mode $Action
    Write-ResultFile -Result $result -Path $ResultPath
    if ($AsJson) { $result | ConvertTo-Json -Depth 8 -Compress } else { Show-Result -Result $result }
    exit 0
} catch {
    $failure = New-FailureResult -ErrorRecord $_ -RequestedAction $Action
    Write-ResultFile -Result $failure -Path $ResultPath
    if ($AsJson) { $failure | ConvertTo-Json -Depth 8 -Compress } else { Show-Result -Result $failure }
    exit 1
}
