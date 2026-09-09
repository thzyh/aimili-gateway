[CmdletBinding()]
param(
    [switch]$Apply,
    [switch]$AsJson,
    [string]$StatePath
)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force

function Test-IPv4PrefixContains {
    param(
        [Parameter(Mandatory)][Net.IPAddress]$Address,
        [Parameter(Mandatory)][string]$Prefix
    )

    $parts = $Prefix.Split('/')
    if ($parts.Count -ne 2) { return $false }
    $network = $null
    $prefixLength = 0
    if (-not [Net.IPAddress]::TryParse($parts[0], [ref]$network) -or
        $network.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or
        -not [int]::TryParse($parts[1], [ref]$prefixLength) -or
        $prefixLength -lt 0 -or $prefixLength -gt 32) {
        return $false
    }

    $addressBytes = $Address.GetAddressBytes()
    $networkBytes = $network.GetAddressBytes()
    $fullBytes = [math]::Floor($prefixLength / 8)
    $remainingBits = $prefixLength % 8
    for ($index = 0; $index -lt $fullBytes; $index++) {
        if ($addressBytes[$index] -ne $networkBytes[$index]) { return $false }
    }
    if ($remainingBits -gt 0) {
        $mask = (0xff -shl (8 - $remainingBits)) -band 0xff
        if (($addressBytes[$fullBytes] -band $mask) -ne ($networkBytes[$fullBytes] -band $mask)) {
            return $false
        }
    }
    return $true
}

function Get-BestHostRoute {
    param([Parameter(Mandatory)][string]$DestinationPrefix)
    $hostRoutes = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix $DestinationPrefix -PolicyStore ActiveStore -ErrorAction SilentlyContinue)
    if ($hostRoutes.Count -eq 0) { return $null }
    return @($hostRoutes | ForEach-Object {
        $ipInterface = Get-NetIPInterface -AddressFamily IPv4 -InterfaceIndex $_.InterfaceIndex -ErrorAction Stop
        [pscustomobject]@{
            Route = $_
            EffectiveMetric = [int]$_.RouteMetric + [int]$ipInterface.InterfaceMetric
        }
    } | Sort-Object EffectiveMetric, @{ Expression = { [int]$_.Route.InterfaceIndex } } | Select-Object -First 1)[0]
}

$paths = Get-AimiliLocalVmPaths
if ([string]::IsNullOrWhiteSpace($StatePath)) {
    $StatePath = Join-Path $paths.RuntimeRoot 'state.json'
}
if (-not (Test-Path -LiteralPath $StatePath -PathType Leaf)) {
    throw 'local_vm_state_missing'
}

$state = Get-Content -LiteralPath $StatePath -Raw | ConvertFrom-Json -ErrorAction Stop
$guestAddress = [string]$state.guestAddress
$sourceAddress = [string]$state.allowedSource
$guestIp = $null
$sourceIp = $null
if (-not [Net.IPAddress]::TryParse($guestAddress, [ref]$guestIp) -or
    $guestIp.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {
    throw 'guest_address_invalid'
}
if (-not [Net.IPAddress]::TryParse($sourceAddress, [ref]$sourceIp) -or
    $sourceIp.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {
    throw 'allowed_source_invalid'
}

$sourceBindings = @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $sourceAddress -ErrorAction Stop)
if ($sourceBindings.Count -ne 1) { throw 'allowed_source_interface_not_unique' }
$targetInterfaceIndex = [int]$sourceBindings[0].InterfaceIndex
$targetAdapter = Get-NetAdapter -IncludeHidden -InterfaceIndex $targetInterfaceIndex -ErrorAction Stop
if ([string]$targetAdapter.InterfaceDescription -notmatch '^VMware Virtual Ethernet Adapter') {
    throw 'allowed_source_is_not_vmware_adapter'
}

$targetDefaultRoutes = @(Get-NetRoute -AddressFamily IPv4 -InterfaceIndex $targetInterfaceIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue)
if ($targetDefaultRoutes.Count -gt 0) { throw 'target_adapter_has_default_route' }

$connectedRoute = @(Get-NetRoute -AddressFamily IPv4 -InterfaceIndex $targetInterfaceIndex -ErrorAction Stop |
    Where-Object {
        $_.NextHop -eq '0.0.0.0' -and
        $_.DestinationPrefix -ne '0.0.0.0/0' -and
        (Test-IPv4PrefixContains -Address $guestIp -Prefix $_.DestinationPrefix)
    } |
    Sort-Object { [int]($_.DestinationPrefix.Split('/')[1]) } -Descending |
    Select-Object -First 1)
if ($connectedRoute.Count -ne 1) { throw 'guest_not_on_vmware_connected_network' }

$desiredPrefix = "$guestAddress/32"
$beforeSafety = Get-AimiliHostSafetySnapshot
$changed = $false
$operationError = $null
try {
    if ($Apply) {
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
        $principal = [Security.Principal.WindowsPrincipal]::new($identity)
        if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
            throw 'host_route_repair_requires_administrator'
        }

        $ipInterface = Get-NetIPInterface -AddressFamily IPv4 -InterfaceIndex $targetInterfaceIndex -ErrorAction Stop
        if ($ipInterface.AutomaticMetric -ne 'Disabled' -or [int]$ipInterface.InterfaceMetric -gt 5) {
            Set-NetIPInterface -AddressFamily IPv4 -InterfaceIndex $targetInterfaceIndex -AutomaticMetric Disabled -InterfaceMetric 5 -ErrorAction Stop
            $changed = $true
        }

        $persistentRoutes = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix $desiredPrefix -InterfaceIndex $targetInterfaceIndex -PolicyStore PersistentStore -ErrorAction SilentlyContinue)
        if ($persistentRoutes.Count -eq 0) {
            $routeOutput = @(& route.exe '-p' 'add' $guestAddress 'mask' '255.255.255.255' '0.0.0.0' 'metric' '1' 'if' ([string]$targetInterfaceIndex) 2>&1)
            if ($LASTEXITCODE -ne 0) { throw "persistent_host_route_add_failed:$LASTEXITCODE" }
            $changed = $true
        } elseif ([int]$persistentRoutes[0].RouteMetric -ne 1) {
            $routeOutput = @(& route.exe '-p' 'change' $guestAddress 'mask' '255.255.255.255' '0.0.0.0' 'metric' '1' 'if' ([string]$targetInterfaceIndex) 2>&1)
            if ($LASTEXITCODE -ne 0) { throw "persistent_host_route_change_failed:$LASTEXITCODE" }
            $changed = $true
        }

        $activeRoutes = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix $desiredPrefix -InterfaceIndex $targetInterfaceIndex -PolicyStore ActiveStore -ErrorAction SilentlyContinue)
        if ($activeRoutes.Count -eq 0) {
            New-NetRoute -AddressFamily IPv4 -DestinationPrefix $desiredPrefix -InterfaceIndex $targetInterfaceIndex -NextHop '0.0.0.0' -RouteMetric 1 -PolicyStore ActiveStore -ErrorAction Stop | Out-Null
            $changed = $true
        } elseif ([int]$activeRoutes[0].RouteMetric -ne 1) {
            $activeRoutes[0] | Set-NetRoute -RouteMetric 1 -ErrorAction Stop | Out-Null
            $changed = $true
        }
    }
} catch {
    $operationError = $_
}

$afterSafety = Get-AimiliHostSafetySnapshot
Assert-AimiliHostSafetyUnchanged -Before $beforeSafety -After $afterSafety
if ($null -ne $operationError) { throw $operationError }

$selectedHostRoute = Get-BestHostRoute -DestinationPrefix $desiredPrefix
$selectedRoute = if ($null -ne $selectedHostRoute) { $selectedHostRoute.Route } else {
    @(Find-NetRoute -RemoteIPAddress $guestAddress -ErrorAction Stop |
        Where-Object { $_.PSObject.Properties['DestinationPrefix'] } |
        Select-Object -Last 1)[0]
}
$selectedInterfaceIndex = [int]$selectedRoute.InterfaceIndex
$persistentReady = @(Get-NetRoute -AddressFamily IPv4 -DestinationPrefix $desiredPrefix -InterfaceIndex $targetInterfaceIndex -PolicyStore PersistentStore -ErrorAction SilentlyContinue).Count -gt 0
$ready = $selectedInterfaceIndex -eq $targetInterfaceIndex -and ((-not $Apply) -or $persistentReady)
$report = [pscustomobject]@{
    ready = $ready
    applied = [bool]$Apply
    changed = $changed
    guestAddress = $guestAddress
    sourceAddress = $sourceAddress
    expectedInterface = [string]$targetAdapter.Name
    expectedInterfaceIndex = $targetInterfaceIndex
    selectedInterface = [string]$selectedRoute.InterfaceAlias
    selectedInterfaceIndex = $selectedInterfaceIndex
    selectedPrefix = [string]$selectedRoute.DestinationPrefix
    persistentRoute = $persistentReady
    persistentRequired = [bool]$Apply
}

if ($AsJson) { $report | ConvertTo-Json -Compress } else { $report }
if (-not $ready) { throw 'host_route_not_selected' }
