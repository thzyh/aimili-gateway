Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:VmwareRoot = 'E:\SoftWare\Vmware16'
$script:VmRoot = 'D:\VirtualMachines\AimiliGatewayLocal'
$script:RuntimeRoot = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AimiliGateway\vmware-local'

function Get-AimiliSha256String {
    param([AllowEmptyString()][string]$Value)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($Value)
        return ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Get-AimiliHostFacts {
    $os = Get-CimInstance Win32_OperatingSystem
    $computer = Get-CimInstance Win32_ComputerSystem
    $processor = Get-CimInstance Win32_Processor | Select-Object -First 1
    $disk = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='D:'"
    if (-not $processor -or -not $disk) {
        throw 'required_host_inventory_missing'
    }

    $vmrun = Join-Path $script:VmwareRoot 'vmrun.exe'
    $runningVmCount = -1
    if (Test-Path -LiteralPath $vmrun -PathType Leaf) {
        $vmrunOutput = @(& $vmrun list 2>&1)
        if ($LASTEXITCODE -eq 0 -and $vmrunOutput.Count -gt 0 -and [string]$vmrunOutput[0] -match '(\d+)') {
            $runningVmCount = [int]$Matches[1]
        }
    }

    [pscustomobject]@{
        LogicalProcessors = [int]$processor.NumberOfLogicalProcessors
        PhysicalCores = [int]$processor.NumberOfCores
        TotalMemoryGiB = [math]::Round([double]$computer.TotalPhysicalMemory / 1GB, 2)
        FreeMemoryGiB = [math]::Round(([double]$os.FreePhysicalMemory * 1KB) / 1GB, 2)
        DFreeGiB = [math]::Round([double]$disk.FreeSpace / 1GB, 2)
        HypervisorPresent = [bool]$computer.HypervisorPresent
        VmwareRoot = $script:VmwareRoot
        RunningVmCount = $runningVmCount
    }
}

function Test-AimiliHostCapacity {
    param([Parameter(Mandatory)]$Facts)

    $reasons = @()
    if ([int]$Facts.LogicalProcessors -lt 4) { $reasons += 'logical_processors_below_4' }
    if ([double]$Facts.FreeMemoryGiB -lt 3.5) { $reasons += 'free_memory_below_3_5_gib' }
    if ([double]$Facts.DFreeGiB -lt 30) { $reasons += 'd_drive_free_below_30_gib' }
    if (-not [bool]$Facts.HypervisorPresent) { $reasons += 'hypervisor_not_present' }
    if (-not (Test-Path -LiteralPath ([string]$Facts.VmwareRoot) -PathType Container)) { $reasons += 'vmware_root_missing' }
    if ([int]$Facts.RunningVmCount -lt 0) { $reasons += 'vmware_inventory_unavailable' }

    [pscustomobject]@{
        Passed = ($reasons.Count -eq 0)
        Reasons = @($reasons)
    }
}

function Get-AimiliHostSafetySnapshot {
    $pids = @(Get-Process -Name 'v2rayN' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id | Sort-Object)
    $internetSettings = Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
    $proxyMaterial = '{0}|{1}|{2}' -f [int]$internetSettings.ProxyEnable, [string]$internetSettings.ProxyServer, [string]$internetSettings.AutoConfigURL

    $routeLines = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction Stop |
        Sort-Object InterfaceIndex, RouteMetric, ifMetric, NextHop |
        ForEach-Object { '{0}|{1}|{2}|{3}' -f $_.InterfaceIndex, $_.RouteMetric, $_.ifMetric, $_.NextHop })

    [pscustomobject]@{
        V2rayNPids = @($pids)
        Proxy = Get-AimiliSha256String -Value $proxyMaterial
        DefaultRoute = Get-AimiliSha256String -Value ($routeLines -join "`n")
    }
}

function Assert-AimiliHostSafetyUnchanged {
    param(
        [Parameter(Mandatory)]$Before,
        [Parameter(Mandatory)]$After
    )

    $beforePids = @($Before.V2rayNPids | Sort-Object) -join ','
    $afterPids = @($After.V2rayNPids | Sort-Object) -join ','
    if ($beforePids -ne $afterPids) { throw 'v2rayn_processes_changed' }
    if ([string]$Before.Proxy -ne [string]$After.Proxy) { throw 'system_proxy_changed' }
    if ([string]$Before.DefaultRoute -ne [string]$After.DefaultRoute) { throw 'default_route_changed' }
}

function Get-AimiliLocalVmPaths {
    [pscustomobject]@{
        VmwareRoot = $script:VmwareRoot
        VmRoot = $script:VmRoot
        RuntimeRoot = $script:RuntimeRoot
        VmxPath = Join-Path $script:VmRoot 'AimiliGatewayLocal.vmx'
    }
}

Export-ModuleMember -Function @(
    'Get-AimiliHostFacts',
    'Test-AimiliHostCapacity',
    'Get-AimiliHostSafetySnapshot',
    'Assert-AimiliHostSafetyUnchanged',
    'Get-AimiliLocalVmPaths'
)
