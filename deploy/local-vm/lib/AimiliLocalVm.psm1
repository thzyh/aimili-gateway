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
    $authorizationService = Get-Service -Name 'VMAuthdService' -ErrorAction SilentlyContinue

    [pscustomobject]@{
        LogicalProcessors = [int]$processor.NumberOfLogicalProcessors
        PhysicalCores = [int]$processor.NumberOfCores
        TotalMemoryGiB = [math]::Round([double]$computer.TotalPhysicalMemory / 1GB, 2)
        FreeMemoryGiB = [math]::Round(([double]$os.FreePhysicalMemory * 1KB) / 1GB, 2)
        DFreeGiB = [math]::Round([double]$disk.FreeSpace / 1GB, 2)
        HypervisorPresent = [bool]$computer.HypervisorPresent
        VmwareRoot = $script:VmwareRoot
        VmwareAuthorizationService = if ($authorizationService) { [string]$authorizationService.Status } else { 'Missing' }
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
    if ([string]$Facts.VmwareAuthorizationService -ne 'Running') { $reasons += 'vmware_authorization_service_not_running' }
    if ([int]$Facts.RunningVmCount -lt 0) { $reasons += 'vmware_inventory_unavailable' }

    [pscustomobject]@{
        Passed = ($reasons.Count -eq 0)
        Reasons = @($reasons)
    }
}

function Get-AimiliHostSafetySnapshot {
    $pids = @(Get-Process -Name 'v2rayN' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id | Sort-Object)
    $internetSettings = Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
    $proxyEnable = if ($internetSettings -and $internetSettings.PSObject.Properties['ProxyEnable']) { [int]$internetSettings.ProxyEnable } else { 0 }
    $proxyServer = if ($internetSettings -and $internetSettings.PSObject.Properties['ProxyServer']) { [string]$internetSettings.ProxyServer } else { '' }
    $autoConfigUrl = if ($internetSettings -and $internetSettings.PSObject.Properties['AutoConfigURL']) { [string]$internetSettings.AutoConfigURL } else { '' }
    $proxyMaterial = '{0}|{1}|{2}' -f $proxyEnable, $proxyServer, $autoConfigUrl

    $routeLines = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction Stop |
        Sort-Object InterfaceIndex, RouteMetric, NextHop |
        ForEach-Object { '{0}|{1}|{2}' -f $_.InterfaceIndex, $_.RouteMetric, $_.NextHop })

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

function Test-AimiliImageLock {
    param([Parameter(Mandatory)]$Lock)

    $url = [string]$Lock.url
    $fileName = [string]$Lock.fileName
    $sha256 = [string]$Lock.sha256
    $sizeBytes = [long]$Lock.sizeBytes
    if ($url -notmatch '^https://cloud-images\.ubuntu\.com/releases/noble/release-20260826/') { throw 'image_url_not_pinned' }
    if ($url -match '/current/') { throw 'image_url_not_pinned' }
    if ([IO.Path]::GetFileName($fileName) -ne $fileName -or $fileName -match '[/\\]') { throw 'image_filename_invalid' }
    if ($sha256 -notmatch '^[0-9a-f]{64}$') { throw 'image_sha256_invalid' }
    if ($sizeBytes -le 0) { throw 'image_size_invalid' }
    return $true
}

function Confirm-AimiliFileDigest {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][long]$Length,
        [Parameter(Mandatory)][string]$Sha256
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $false }
    $item = Get-Item -LiteralPath $Path
    if ([long]$item.Length -ne $Length) { return $false }
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    return $actual -eq $Sha256.ToLowerInvariant()
}

function Get-AimiliPhysicalBridgePlan {
    $physical = @(Get-NetAdapter -Physical -ErrorAction Stop |
        Where-Object { $_.Status -eq 'Up' -and $_.InterfaceDescription -notmatch 'VMware|Hyper-V|Virtual|Tunnel' } |
        Sort-Object LinkSpeed -Descending)
    if ($physical.Count -ne 1) { throw 'physical_bridge_adapter_not_unique' }
    $binding = Get-NetAdapterBinding -Name $physical[0].Name -ComponentID 'vmware_bridge' -ErrorAction Stop
    if (-not $binding.Enabled) { throw 'vmware_bridge_binding_disabled' }
    $address = Get-NetIPAddress -InterfaceIndex $physical[0].ifIndex -AddressFamily IPv4 -ErrorAction Stop |
        Where-Object { $_.IPAddress -notlike '169.254.*' } |
        Select-Object -First 1
    if (-not $address) { throw 'physical_bridge_ipv4_missing' }
    $parsed = [Net.IPAddress]::Parse($address.IPAddress)
    $bytes = $parsed.GetAddressBytes()
    $isPrivate = $bytes[0] -eq 10 -or ($bytes[0] -eq 172 -and $bytes[1] -ge 16 -and $bytes[1] -le 31) -or ($bytes[0] -eq 192 -and $bytes[1] -eq 168)
    if (-not $isPrivate) { throw 'physical_bridge_source_not_private' }
    [pscustomobject]@{
        Ready = $true
        AdapterName = $physical[0].Name
        InterfaceDescription = $physical[0].InterfaceDescription
        SourceAddress = $address.IPAddress
    }
}

function New-AimiliVmPlan {
    param(
        [Parameter(Mandatory)]$Facts,
        [Parameter(Mandatory)]$BridgePlan
    )
    $capacity = Test-AimiliHostCapacity -Facts $Facts
    if (-not $capacity.Passed) { throw ('host_capacity_rejected:' + (@($capacity.Reasons) -join ',')) }
    [pscustomobject]@{
        Vcpus = 2
        MemoryMiB = 2048
        DiskGiB = 24
        DiskMode = 'monolithicSparse'
        SwapGiB = 1
        Networks = @([pscustomobject]@{ Type='bridged'; Name='VMnet0'; DefaultRoute=$true; AllowedSource=$BridgePlan.SourceAddress })
    }
}

function New-AimiliCloudInitPayload {
    param(
        [Parameter(Mandatory)][string]$PublicKey,
        [Parameter(Mandatory)][string]$WanMac,
        [Parameter(Mandatory)][string]$AllowedSource,
        [Parameter(Mandatory)][string]$InstanceId
    )
    if ($PublicKey -notmatch '^ssh-ed25519\s+[A-Za-z0-9+/=]+(?:\s+.*)?$') { throw 'ssh_public_key_invalid' }
    if ($WanMac -notmatch '^00:50:56:[0-3][0-9a-f]:[0-9a-f]{2}:[0-9a-f]{2}$') { throw 'vmware_mac_invalid' }
    if ($AllowedSource -notmatch '^\d{1,3}(?:\.\d{1,3}){3}$') { throw 'allowed_source_invalid' }
    if ($InstanceId -notmatch '^[a-z0-9-]{8,64}$') { throw 'instance_id_invalid' }

    $userData = @"
#cloud-config
hostname: aimili-gateway-local
manage_etc_hosts: true
users:
  - name: aimili
    gecos: Aimili Local Runtime
    groups: [adm, sudo]
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true
    ssh_authorized_keys:
      - $PublicKey
ssh_pwauth: false
disable_root: true
package_update: false
packages: [ufw]
growpart:
  mode: auto
  devices: ['/']
resize_rootfs: true
swap:
  filename: /swap.img
  size: 1073741824
runcmd:
  - [systemctl, enable, --now, ssh]
  - [ufw, default, deny, incoming]
  - [ufw, default, allow, outgoing]
  - [ufw, allow, from, "$AllowedSource", to, any, port, "22", proto, tcp]
  - [sh, -c, "ufw --force enable"]
final_message: aimili-local-cloud-init-complete
"@
    $metaData = @"
instance-id: $InstanceId
local-hostname: aimili-gateway-local
"@
    $networkConfig = @"
version: 2
ethernets:
  primary:
    match:
      macaddress: "$WanMac"
    set-name: ens192
    dhcp4: true
    dhcp6: false
"@
    [pscustomobject]@{
        InstanceId = $InstanceId
        UserData = $userData
        UserDataBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($userData))
        MetaData = $metaData
        NetworkConfig = $networkConfig
    }
}

function New-AimiliNoCloudSeedImage {
    param(
        [Parameter(Mandatory)]$Payload,
        [Parameter(Mandatory)][string]$MkisofsPath,
        [Parameter(Mandatory)][string]$OutputPath
    )
    if (-not (Test-Path -LiteralPath $MkisofsPath -PathType Leaf)) { throw 'mkisofs_missing' }
    foreach ($propertyName in @('UserData', 'MetaData', 'NetworkConfig')) {
        if (-not $Payload.PSObject.Properties[$propertyName] -or [string]::IsNullOrWhiteSpace([string]$Payload.$propertyName)) {
            throw 'nocloud_payload_incomplete'
        }
    }

    $resolvedOutput = [IO.Path]::GetFullPath($OutputPath)
    $outputDirectory = [IO.Path]::GetDirectoryName($resolvedOutput)
    if ([string]::IsNullOrWhiteSpace($outputDirectory)) { throw 'nocloud_output_directory_invalid' }
    [IO.Directory]::CreateDirectory($outputDirectory) | Out-Null
    $sourceDirectory = "$resolvedOutput.source"
    [IO.Directory]::CreateDirectory($sourceDirectory) | Out-Null
    [IO.File]::WriteAllText((Join-Path $sourceDirectory 'user-data'), [string]$Payload.UserData, [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $sourceDirectory 'meta-data'), [string]$Payload.MetaData, [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $sourceDirectory 'network-config'), [string]$Payload.NetworkConfig, [Text.UTF8Encoding]::new($false))

    $partialPath = "$resolvedOutput.part"
    if (Test-Path -LiteralPath $partialPath) { Remove-Item -LiteralPath $partialPath -Force }
    Push-Location $sourceDirectory
    try {
        & $MkisofsPath -quiet -volid cidata -joliet -rock -output $partialPath 'user-data' 'meta-data' 'network-config'
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $partialPath -PathType Leaf)) { throw 'nocloud_seed_creation_failed' }
    } finally {
        Pop-Location
    }
    Move-Item -LiteralPath $partialPath -Destination $resolvedOutput -Force
    [pscustomobject]@{
        IsoPath = $resolvedOutput
        Sha256 = (Get-FileHash -LiteralPath $resolvedOutput -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

function Get-AimiliNoCloudVmxValues {
    param([Parameter(Mandatory)][string]$SeedPath)
    $resolvedSeedPath = [IO.Path]::GetFullPath($SeedPath)
    if (-not (Test-Path -LiteralPath $resolvedSeedPath -PathType Leaf)) { throw 'nocloud_seed_missing' }
    [ordered]@{
        'ide1:0.present' = 'TRUE'
        'ide1:0.deviceType' = 'cdrom-image'
        'ide1:0.fileName' = $resolvedSeedPath
        'ide1:0.startConnected' = 'TRUE'
        'ide1:0.clientDevice' = 'FALSE'
    }
}

function New-AimiliVmwareMac {
    $bytes = New-Object byte[] 3
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $bytes[0] = $bytes[0] -band 0x3F
    return '00:50:56:{0:x2}:{1:x2}:{2:x2}' -f $bytes[0], $bytes[1], $bytes[2]
}

function Set-AimiliVmxValues {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][Collections.IDictionary]$Values
    )
    $lines = [Collections.Generic.List[string]]::new()
    foreach ($line in [IO.File]::ReadAllLines($Path)) { $lines.Add($line) }
    foreach ($key in $Values.Keys) {
        $replacement = '{0} = "{1}"' -f $key, ([string]$Values[$key]).Replace('"', '\"')
        $found = $false
        for ($index = 0; $index -lt $lines.Count; $index++) {
            if ($lines[$index] -match ('^\s*' + [regex]::Escape([string]$key) + '\s*=')) {
                $lines[$index] = $replacement
                $found = $true
                break
            }
        }
        if (-not $found) { $lines.Add($replacement) }
    }
    [IO.File]::WriteAllLines($Path, $lines, [Text.UTF8Encoding]::new($false))
}

function Get-AimiliNativeManifest {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { throw 'native_manifest_missing' }
    $manifest = Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json
    if ([int]$manifest.schemaVersion -ne 1) { throw 'native_manifest_schema_invalid' }
    $requiredServices = @('aimilivpn.service', 'x-ui.service', 'aimili-gateway.service', 'caddy.service')
    foreach ($service in $requiredServices) {
        if (@($manifest.services) -notcontains $service) { throw "native_manifest_service_missing:$service" }
    }
    foreach ($name in @('openvpn', 'xray', 'logicalExits', 'exitSlots')) {
        $value = $manifest.expected.$name
        if ($null -eq $value -or [int]$value -lt 0 -or [int]$value -ne [double]$value) { throw "native_manifest_expected_invalid:$name" }
    }
    $ports = @($manifest.ports.PSObject.Properties | ForEach-Object { [int]$_.Value })
    if ($ports.Count -eq 0 -or @($ports | Where-Object { $_ -lt 1 -or $_ -gt 65535 }).Count -gt 0) { throw 'native_manifest_ports_invalid' }
    if (@($ports | Sort-Object -Unique).Count -ne $ports.Count) { throw 'native_manifest_ports_duplicate' }
    if ([string]::IsNullOrWhiteSpace([string]$manifest.sourceCommit)) { throw 'native_manifest_source_commit_missing' }
    return $manifest
}

function Assert-AimiliNativeStatusContract {
    param([Parameter(Mandatory)]$Status)
    foreach ($name in @('nativeServices', 'nativeEnabled', 'expected', 'actual', 'listeners', 'mainChecks', 'slotChecks', 'databaseReadable', 'evidenceSchema', 'subscriptionExitSet', 'protocolIsolation', 'hostSafety', 'nativeReady')) {
        if (-not $Status.PSObject.Properties[$name]) { throw "native_status_field_missing:$name" }
    }
    foreach ($name in @('openvpn', 'xray', 'logicalExits', 'exitSlots')) {
        foreach ($section in @('expected', 'actual')) {
            $value = $Status.$section.$name
            if ($null -eq $value -or [int]$value -lt 0 -or [int]$value -ne [double]$value) { throw "native_status_count_invalid:$section.$name" }
        }
    }
    foreach ($service in @('aimilivpn', 'xui', 'gateway', 'caddy')) {
        if (-not $Status.nativeServices.PSObject.Properties[$service]) { throw "native_status_service_missing:$service" }
        if (-not $Status.nativeEnabled.PSObject.Properties[$service]) { throw "native_status_enabled_missing:$service" }
    }
    return $true
}

Export-ModuleMember -Function @(
    'Get-AimiliHostFacts',
    'Test-AimiliHostCapacity',
    'Get-AimiliHostSafetySnapshot',
    'Assert-AimiliHostSafetyUnchanged',
    'Get-AimiliLocalVmPaths',
    'Test-AimiliImageLock',
    'Confirm-AimiliFileDigest',
    'Get-AimiliPhysicalBridgePlan',
    'New-AimiliVmPlan',
    'New-AimiliCloudInitPayload',
    'New-AimiliNoCloudSeedImage',
    'Get-AimiliNoCloudVmxValues',
    'New-AimiliVmwareMac',
    'Set-AimiliVmxValues',
    'Get-AimiliNativeManifest',
    'Assert-AimiliNativeStatusContract'
)
