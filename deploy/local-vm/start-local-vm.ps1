[CmdletBinding()]
param(
    [ValidateSet('Menu', 'Start', 'Status', 'RepairRoute', 'Stop')]
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
        TrayPath = Join-Path $paths.VmwareRoot 'vmware-tray.exe'
        RepairPath = Join-Path $PSScriptRoot 'repair-host-route.ps1'
        StatusPath = Join-Path $PSScriptRoot 'status.ps1'
    }
    foreach ($requiredPath in @($inputs.StatePath, $inputs.KeyPath, $inputs.KnownHosts, $inputs.VmrunPath, $inputs.TrayPath, $paths.VmxPath, $inputs.RepairPath, $inputs.StatusPath)) {
        if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
            throw "local_vm_startup_input_missing:$requiredPath"
        }
    }
    return $inputs
}

function Get-RunningVmPaths {
    param([Parameter(Mandatory)]$Inputs)
    $output = @(& $Inputs.VmrunPath 'list' 2>&1)
    if ($LASTEXITCODE -ne 0) { throw "vmrun_list_failed:$LASTEXITCODE" }
    return @($output | Select-Object -Skip 1 | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_) })
}

function Get-CurrentSessionTrayProcesses {
    $sessionId = [Diagnostics.Process]::GetCurrentProcess().SessionId
    return @(Get-Process -Name 'vmware-tray' -ErrorAction SilentlyContinue | Where-Object { $_.SessionId -eq $sessionId })
}

function Start-VMwareTray {
    param([Parameter(Mandatory)]$Inputs)
    $script:CurrentStage = 'vmware-tray'
    $trayProcesses = @(Get-CurrentSessionTrayProcesses)
    if ($trayProcesses.Count -eq 0) {
        Start-Process -FilePath $Inputs.TrayPath -WindowStyle Hidden | Out-Null
        $deadline = (Get-Date).AddSeconds(10)
        do {
            Start-Sleep -Milliseconds 250
            $trayProcesses = @(Get-CurrentSessionTrayProcesses)
        } while ($trayProcesses.Count -eq 0 -and (Get-Date) -lt $deadline)
    }
    return $trayProcesses.Count -gt 0
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
    param([ValidateSet('Start', 'Status', 'RepairRoute', 'Stop')][string]$Mode)
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

    if ($Mode -eq 'Stop') {
        $script:CurrentStage = 'vmware-shutdown'
        $runningVmPaths = @(Get-RunningVmPaths -Inputs $inputs)
        $vmWasRunning = $runningVmPaths -contains $paths.VmxPath
        if ($vmWasRunning) {
            $stopOutput = @(& $inputs.VmrunPath 'stop' $paths.VmxPath 'soft' 2>&1)
            if ($LASTEXITCODE -ne 0) { throw "vmrun_soft_stop_failed:$LASTEXITCODE" }
            $deadline = (Get-Date).AddSeconds(180)
            do {
                Start-Sleep -Seconds 3
                $runningVmPaths = @(Get-RunningVmPaths -Inputs $inputs)
            } while ($runningVmPaths -contains $paths.VmxPath -and (Get-Date) -lt $deadline)
            if ($runningVmPaths -contains $paths.VmxPath) { throw 'local_vm_soft_stop_timeout' }
        }

        $otherRunningVms = @($runningVmPaths | Where-Object { $_ -ne $paths.VmxPath })
        $sharedServicesStopped = $false
        $trayStopped = $false
        if ($otherRunningVms.Count -eq 0) {
            $script:CurrentStage = 'vmware-tray'
            $trayProcesses = @(Get-CurrentSessionTrayProcesses)
            if ($trayProcesses.Count -gt 0) {
                $trayProcesses | Stop-Process -Force -ErrorAction Stop
                $trayStopped = $true
            }
            $script:CurrentStage = 'vmware-services'
            foreach ($serviceName in @('VMware NAT Service', 'VMnetDHCP', 'VMAuthdService')) {
                $service = Get-Service -Name $serviceName -ErrorAction Stop
                if ($service.Status -ne [ServiceProcess.ServiceControllerStatus]::Stopped) {
                    Stop-Service -Name $serviceName -ErrorAction Stop
                    $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(30))
                }
            }
            $sharedServicesStopped = $true
        }
        $script:CurrentStage = 'complete'
        return [pscustomobject][ordered]@{
            success = $true; action = $Mode; stage = 'complete'
            vmWasRunning = $vmWasRunning; vmRunning = $false
            otherRunningVms = $otherRunningVms.Count
            sharedServicesStopped = $sharedServicesStopped; trayStopped = $trayStopped
            persistentRoutePreserved = $true
            elapsedSeconds = [math]::Round(([DateTimeOffset]::Now - $startedAt).TotalSeconds, 1)
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
    $vmRunning = @(Get-RunningVmPaths -Inputs $inputs) -contains $paths.VmxPath
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

    $trayRunning = @(Get-CurrentSessionTrayProcesses).Count -gt 0
    if ($Mode -eq 'Start') { $trayRunning = Start-VMwareTray -Inputs $inputs }
    $script:CurrentStage = 'complete'
    return [pscustomobject][ordered]@{
        success = $true; action = $Mode; stage = 'complete'; vmStarted = $vmStarted; vmRunning = $true
        vmxPath = $paths.VmxPath; services = [pscustomobject]$serviceStates
        hostRoute = [pscustomobject][ordered]@{
            ready = [bool]$route.ready; persistent = [bool]$route.persistentRoute
            selectedInterface = [string]$route.selectedInterface
            guestAddress = [string]$route.guestAddress; sourceAddress = [string]$route.sourceAddress
        }
        sshReachable = $sshReachable; nativeReady = [bool]$nativeStatus.nativeReady; trayRunning = $trayRunning
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
    param([ValidateSet('Start', 'RepairRoute', 'Stop')][string]$Mode)
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
    param([ValidateSet('Start', 'Status', 'RepairRoute', 'Stop')][string]$Mode)
    if ($Mode -in @('Start', 'RepairRoute', 'Stop') -and -not (Test-Administrator)) {
        return Invoke-ElevatedOperation -Mode $Mode
    }
    return Invoke-LocalVmOperation -Mode $Mode
}

function Show-Result {
    param([Parameter(Mandatory)]$Result)
    if (-not $Result.success) {
        Write-Host "操作失败，阶段：$($Result.stage)" -ForegroundColor Red
        Write-Host "错误：$($Result.error)" -ForegroundColor Red
        if ($Result.diagnostic) { Write-Host "诊断信息：$($Result.diagnostic)" -ForegroundColor DarkGray }
        return
    }
    if ($Result.action -eq 'RepairRoute') {
        Write-Host '虚拟机主机路由已经就绪。' -ForegroundColor Green
        Write-Host "虚拟机地址：$($Result.hostRoute.guestAddress)"
        Write-Host "使用网卡：$($Result.hostRoute.selectedInterface)"
        Write-Host "持久路由：$($Result.hostRoute.persistent)"
        return
    }
    if ($Result.action -eq 'Stop') {
        Write-Host 'AimiliGatewayLocal 已安全关闭。' -ForegroundColor Green
        Write-Host "本次关闭前虚拟机正在运行：$($Result.vmWasRunning)"
        Write-Host "其他正在运行的 VMware 虚拟机：$($Result.otherRunningVms)"
        Write-Host "共享 VMware 服务已停止：$($Result.sharedServicesStopped)"
        Write-Host "VMware 托盘程序已停止：$($Result.trayStopped)"
        Write-Host "VMnet8 持久路由已保留：$($Result.persistentRoutePreserved)"
        return
    }
    Write-Host 'AimiliGatewayLocal 已经就绪。' -ForegroundColor Green
    Write-Host "本次是否启动了虚拟机：$($Result.vmStarted)"
    Write-Host "SSH 是否可达：$($Result.sshReachable)"
    Write-Host "业务数据面是否就绪：$($Result.nativeReady)"
    Write-Host "VMware 托盘是否运行：$($Result.trayRunning)"
    Write-Host "OpenVPN/Xray/逻辑出口：$($Result.actual.openvpn)/$($Result.actual.xray)/$($Result.actual.logicalExits)"
    Write-Host "Gateway 地址：$($Result.gatewayUrl)"
}

if ($ValidateOnly) { $Action = 'Status' }

if ($Action -eq 'Menu') {
    do {
        Clear-Host
        Write-Host 'AimiliGatewayLocal 启动管理器' -ForegroundColor Cyan
        Write-Host ''
        Write-Host '1. 启动或恢复 AimiliGatewayLocal，并等待全部服务就绪'
        Write-Host '2. 只读检查当前运行状态'
        Write-Host '3. 修复或检查 VMnet8 持久主机路由'
        Write-Host '4. 停止 AimiliGatewayLocal，并在安全时关闭 VMware 服务'
        Write-Host '0. 退出'
        Write-Host ''
        $choice = Read-Host '请选择'
        if ($choice -eq '0') { break }
        $selected = switch ($choice) {
            '1' { 'Start' }; '2' { 'Status' }; '3' { 'RepairRoute' }; '4' { 'Stop' }; default { $null }
        }
        if ($null -eq $selected) {
            Write-Host '输入无效，请输入 0、1、2、3 或 4。' -ForegroundColor Yellow
        } else {
            try {
                if ($selected -eq 'Stop') {
                    $confirmation = Read-Host '确认关闭 AimiliGatewayLocal 吗？请输入 Y 继续'
                    if ($confirmation -notin @('Y', 'y')) {
                        Write-Host '已取消关闭操作。' -ForegroundColor Yellow
                        $selected = $null
                    }
                }
                if ($null -ne $selected) {
                    Write-Host '正在执行，请稍候……' -ForegroundColor Cyan
                    Show-Result -Result (Invoke-RequestedAction -Mode $selected)
                }
            } catch {
                Show-Result -Result (New-FailureResult -ErrorRecord $_ -RequestedAction $selected)
            }
        }
        Write-Host ''
        [void](Read-Host '按 Enter 键返回菜单')
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
