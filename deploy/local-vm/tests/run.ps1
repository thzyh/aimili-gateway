[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$modulePath = Join-Path $PSScriptRoot '..\lib\AimiliLocalVm.psm1'
if (-not (Test-Path -LiteralPath $modulePath -PathType Leaf)) {
    throw 'Production module is missing: AimiliLocalVm.psm1'
}
Import-Module $modulePath -Force

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}

function Assert-Equal {
    param($Expected, $Actual, [string]$Message)
    if ($Expected -ne $Actual) {
        throw "$Message (expected=$Expected actual=$Actual)"
    }
}

function New-NativeManifestFixture {
    param([int]$OpenVpn, [int]$Xray, [int]$LogicalExits, [int]$ExitSlots)
    $path = Join-Path ([IO.Path]::GetTempPath()) ("aimili-native-manifest-{0}.json" -f [guid]::NewGuid().ToString('N'))
    [ordered]@{
        schemaVersion = 1
        services = @('aimilivpn.service', 'x-ui.service', 'aimili-gateway.service', 'caddy.service')
        expected = [ordered]@{
            openvpn = $OpenVpn
            xray = $Xray
            logicalExits = $LogicalExits
            exitSlots = $ExitSlots
        }
        ports = [ordered]@{ aimilivpnUi = 8787; aimilivpnProxy = 7928; control = 8790; xuiPanel = 2001; xuiSubscription = 2096; gateway = 9080; caddy = 8080 }
        sourceCommit = 'fixture-commit'
    } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $path -Encoding utf8NoBOM
    return $path
}

$manifestPath = New-NativeManifestFixture -OpenVpn 4 -Xray 1 -LogicalExits 4 -ExitSlots 3
$manifestExtraScalePath = New-NativeManifestFixture -OpenVpn 8 -Xray 2 -LogicalExits 8 -ExitSlots 7
try {
    $manifest = Get-AimiliNativeManifest -Path $manifestPath
    Assert-Equal 4 $manifest.expected.openvpn 'native manifest rejected current OpenVPN expectation'
    Assert-Equal 1 $manifest.expected.xray 'native manifest rejected current Xray expectation'
    $manifestExtraScale = Get-AimiliNativeManifest -Path $manifestExtraScalePath
    Assert-Equal 8 $manifestExtraScale.expected.openvpn 'native manifest imposed an OpenVPN hard limit'
    Assert-Equal 2 $manifestExtraScale.expected.xray 'native manifest imposed an Xray hard limit'
    Assert-AimiliNativeStatusContract -Status ([pscustomobject]@{
        nativeServices = [pscustomobject]@{ aimilivpn = 'inactive'; xui = 'inactive'; gateway = 'inactive'; caddy = 'inactive' }
        nativeEnabled = [pscustomobject]@{ aimilivpn = 'disabled'; xui = 'disabled'; gateway = 'disabled'; caddy = 'disabled' }
        expected = [pscustomobject]@{ openvpn = 4; xray = 1; logicalExits = 4; exitSlots = 3 }
        actual = [pscustomobject]@{ openvpn = 0; xray = 0; logicalExits = 0; exitSlots = 0 }
        listeners = [pscustomobject]@{}
        mainChecks = [pscustomobject]@{ tun = $false; route = $false; listener = $false; egress = $false }
        slotChecks = @()
        databaseReadable = $false
        evidenceSchema = $false
        subscriptionExitSet = $false
        protocolIsolation = $false
        hostSafety = $false
        nativeReady = $false
    })
    $shallowRejected = $false
    try {
        Assert-AimiliNativeStatusContract -Status ([pscustomobject]@{
            nativeServices = [pscustomobject]@{ aimilivpn = 'active'; xui = 'active'; gateway = 'active'; caddy = 'active' }
            nativeEnabled = [pscustomobject]@{ aimilivpn = 'enabled'; xui = 'enabled'; gateway = 'enabled'; caddy = 'enabled' }
            expected = [pscustomobject]@{ openvpn = 4; xray = 1; logicalExits = 4; exitSlots = 3 }
            actual = [pscustomobject]@{ openvpn = 4; xray = 1; logicalExits = 4; exitSlots = 3 }
            nativeReady = $true
        })
    } catch { $shallowRejected = $_.Exception.Message -match 'native_status_field_missing' }
    Assert-True $shallowRejected 'native status contract accepted shallow readiness without data-plane and evidence fields'
} finally {
    foreach ($fixture in @($manifestPath, $manifestExtraScalePath)) {
        if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture -Force }
    }
}

$good = [pscustomobject]@{
    LogicalProcessors = 12
    FreeMemoryGiB = 3.85
    DFreeGiB = 129
    HypervisorPresent = $true
    VmwareRoot = 'E:\SoftWare\Vmware16'
    VmwareAuthorizationService = 'Running'
    RunningVmCount = 0
}
$goodResult = Test-AimiliHostCapacity -Facts $good
Assert-True $goodResult.Passed 'safe host capacity was rejected'
Assert-Equal 0 @($goodResult.Reasons).Count 'safe host returned rejection reasons'

$lowMemory = [pscustomobject]@{
    LogicalProcessors = 12
    FreeMemoryGiB = 3.49
    DFreeGiB = 129
    HypervisorPresent = $true
    VmwareRoot = 'E:\SoftWare\Vmware16'
    VmwareAuthorizationService = 'Running'
    RunningVmCount = 0
}
$lowResult = Test-AimiliHostCapacity -Facts $lowMemory
Assert-True (-not $lowResult.Passed) 'host below the memory floor was accepted'
Assert-True (@($lowResult.Reasons) -contains 'free_memory_below_3_5_gib') 'low memory reason was not reported'

$stoppedAuthorization = [pscustomobject]@{
    LogicalProcessors = 12
    FreeMemoryGiB = 3.85
    DFreeGiB = 129
    HypervisorPresent = $true
    VmwareRoot = 'E:\SoftWare\Vmware16'
    VmwareAuthorizationService = 'Stopped'
    RunningVmCount = 0
}
$stoppedResult = Test-AimiliHostCapacity -Facts $stoppedAuthorization
Assert-True (-not $stoppedResult.Passed) 'stopped VMware authorization service was accepted'
Assert-True (@($stoppedResult.Reasons) -contains 'vmware_authorization_service_not_running') 'stopped VMware authorization service reason was not reported'

$before = [pscustomobject]@{
    V2rayNPids = @(10, 20)
    Proxy = '1|127.0.0.1:10808'
    DefaultRoute = 'if=4;metric=25'
}
$same = [pscustomobject]@{
    V2rayNPids = @(20, 10)
    Proxy = '1|127.0.0.1:10808'
    DefaultRoute = 'if=4;metric=25'
}
Assert-AimiliHostSafetyUnchanged -Before $before -After $same

$changed = [pscustomobject]@{
    V2rayNPids = @(10, 20)
    Proxy = '1|127.0.0.1:10808'
    DefaultRoute = 'if=9;metric=1'
}
$routeChangeRejected = $false
try {
    Assert-AimiliHostSafetyUnchanged -Before $before -After $changed
} catch {
    $routeChangeRejected = $_.Exception.Message -match 'default_route_changed'
}
Assert-True $routeChangeRejected 'default route mutation was accepted'

$liveSnapshot = Get-AimiliHostSafetySnapshot
Assert-Equal 64 ([string]$liveSnapshot.Proxy).Length 'live proxy snapshot is not a SHA256 digest'
Assert-Equal 64 ([string]$liveSnapshot.DefaultRoute).Length 'live route snapshot is not a SHA256 digest'

$imageLockPath = Join-Path $PSScriptRoot '..\image-lock.json'
if (-not (Test-Path -LiteralPath $imageLockPath -PathType Leaf)) {
    throw 'Production image lock is missing: image-lock.json'
}
$imageLock = Get-Content -LiteralPath $imageLockPath -Raw | ConvertFrom-Json
Assert-True (Test-AimiliImageLock -Lock $imageLock) 'pinned Ubuntu image lock was rejected'

$rollingLock = [pscustomobject]@{
    url = 'https://cloud-images.ubuntu.com/noble/current/ubuntu.ova'
    fileName = 'ubuntu.ova'
    sha256 = 'f097111f88c9e3973057e1530363e6be6d9db1446e97280f0512cf77952b12d0'
    sizeBytes = 14
}
$rollingRejected = $false
try { Test-AimiliImageLock -Lock $rollingLock | Out-Null } catch { $rollingRejected = $_.Exception.Message -match 'image_url_not_pinned' }
Assert-True $rollingRejected 'rolling image URL was accepted'

$fixturePath = Join-Path ([IO.Path]::GetTempPath()) ("aimili-image-fixture-{0}.txt" -f [guid]::NewGuid().ToString('N'))
try {
    [IO.File]::WriteAllText($fixturePath, 'aimili-fixture', [Text.UTF8Encoding]::new($false))
    Assert-True (Confirm-AimiliFileDigest -Path $fixturePath -Length 14 -Sha256 'f097111f88c9e3973057e1530363e6be6d9db1446e97280f0512cf77952b12d0') 'valid fixture digest was rejected'
    Assert-True (-not (Confirm-AimiliFileDigest -Path $fixturePath -Length 14 -Sha256 ('0' * 64))) 'invalid fixture digest was accepted'
} finally {
    if (Test-Path -LiteralPath $fixturePath) { Remove-Item -LiteralPath $fixturePath -Force }
}

$bridgePlan = [pscustomobject]@{
    Ready = $true
    AdapterName = 'Ethernet Fixture'
    InterfaceDescription = 'Physical Ethernet Fixture'
    SourceAddress = '192.0.2.10'
}
$vmPlan = New-AimiliVmPlan -Facts $good -BridgePlan $bridgePlan
Assert-Equal 2 $vmPlan.Vcpus 'VM plan changed the CPU limit'
Assert-Equal 2048 $vmPlan.MemoryMiB 'VM plan changed the memory limit'
Assert-Equal 24 $vmPlan.DiskGiB 'VM plan changed the disk limit'
Assert-Equal 'monolithicSparse' $vmPlan.DiskMode 'VM plan uses a disk mode unsupported by Workstation VMX targets'
Assert-Equal 'bridged' $vmPlan.Networks[0].Type 'first VM NIC is not bridged'
Assert-Equal 1 @($vmPlan.Networks).Count 'VM plan added an unexpected second NIC'

$fixtureKey = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureOnlyKeyMaterial1234567890 aimili-test'
$cloudInit = New-AimiliCloudInitPayload -PublicKey $fixtureKey -WanMac '00:50:56:01:02:03' -AllowedSource $bridgePlan.SourceAddress -InstanceId 'aimili-test-instance'
Assert-True ($cloudInit.UserData -match [regex]::Escape($fixtureKey)) 'cloud-init omitted the requested SSH public key'
Assert-True ($cloudInit.UserData -match 'ssh_pwauth:\s*false') 'cloud-init did not disable password SSH'
Assert-True ($cloudInit.UserData -match 'disable_root:\s*true') 'cloud-init did not disable root login'
Assert-True ($cloudInit.UserData -match [regex]::Escape($bridgePlan.SourceAddress)) 'cloud-init omitted the allowed Windows source'
Assert-True ($cloudInit.UserData -match 'ufw --force enable') 'cloud-init did not enable the source firewall'
Assert-True ($cloudInit.UserDataBase64 -match '^[A-Za-z0-9+/]+=*$') 'cloud-init user-data was not base64 encoded'
Assert-True ($cloudInit.MetaData -match 'instance-id:\s*aimili-test-instance') 'NoCloud metadata omitted the stable instance ID'
Assert-True ($cloudInit.MetaData -match 'local-hostname:\s*aimili-gateway-local') 'NoCloud metadata omitted the local hostname'
Assert-True ($cloudInit.NetworkConfig -match 'macaddress:\s*"00:50:56:01:02:03"') 'NoCloud network config does not match the managed VM MAC'
Assert-True ($cloudInit.NetworkConfig -match 'dhcp4:\s*true') 'NoCloud network config did not enable IPv4 DHCP'
Assert-True ($cloudInit.NetworkConfig -match 'set-name:\s*ens192') 'NoCloud network config did not stabilize the VMware interface name'

$seedFixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("aimili-seed-fixture-{0}" -f [guid]::NewGuid().ToString('N'))
$seedFixtureIso = Join-Path $seedFixtureRoot 'seed.iso'
try {
    New-Item -ItemType Directory -Path $seedFixtureRoot | Out-Null
    $seedResult = New-AimiliNoCloudSeedImage -Payload $cloudInit -MkisofsPath (Join-Path $good.VmwareRoot 'mkisofs.exe') -OutputPath $seedFixtureIso
    Assert-True (Test-Path -LiteralPath $seedResult.IsoPath -PathType Leaf) 'NoCloud seed image was not created'
    Assert-True ((Get-Item -LiteralPath $seedResult.IsoPath).Length -gt 0) 'NoCloud seed image is empty'
    $seedEntries = @(tar -tf $seedResult.IsoPath)
    Assert-True ($seedEntries -contains 'user-data') 'NoCloud seed image omitted user-data'
    Assert-True ($seedEntries -contains 'meta-data') 'NoCloud seed image omitted meta-data'
    Assert-True ($seedEntries -contains 'network-config') 'NoCloud seed image omitted network-config'
    $seedVmxValues = Get-AimiliNoCloudVmxValues -SeedPath $seedResult.IsoPath
    Assert-Equal 'TRUE' $seedVmxValues['ide1:0.present'] 'NoCloud seed CD-ROM is not present'
    Assert-Equal 'cdrom-image' $seedVmxValues['ide1:0.deviceType'] 'NoCloud seed is not mounted as an ISO image'
    Assert-Equal $seedResult.IsoPath $seedVmxValues['ide1:0.fileName'] 'NoCloud seed VMX mapping changed the ISO path'
    Assert-Equal 'TRUE' $seedVmxValues['ide1:0.startConnected'] 'NoCloud seed CD-ROM is not connected at boot'
    Assert-Equal 'FALSE' $seedVmxValues['ide1:0.clientDevice'] 'NoCloud seed CD-ROM incorrectly depends on a host client device'
} finally {
    foreach ($fixtureName in @('seed.iso', 'seed.iso.part')) {
        $fixtureItem = Join-Path $seedFixtureRoot $fixtureName
        if (Test-Path -LiteralPath $fixtureItem) { Remove-Item -LiteralPath $fixtureItem -Force }
    }
    $seedSourceRoot = "$seedFixtureIso.source"
    foreach ($fixtureName in @('user-data', 'meta-data', 'network-config')) {
        $fixtureItem = Join-Path $seedSourceRoot $fixtureName
        if (Test-Path -LiteralPath $fixtureItem) { Remove-Item -LiteralPath $fixtureItem -Force }
    }
    if (Test-Path -LiteralPath $seedSourceRoot) { Remove-Item -LiteralPath $seedSourceRoot -Force }
    if (Test-Path -LiteralPath $seedFixtureRoot) { Remove-Item -LiteralPath $seedFixtureRoot -Force }
}

$createVmPath = Join-Path $PSScriptRoot '..\create-vm.ps1'
if (-not (Test-Path -LiteralPath $createVmPath -PathType Leaf)) {
    throw 'Production VM creation script is missing: create-vm.ps1'
}
$pathsBefore = Get-AimiliLocalVmPaths
$vmxExistedBefore = Test-Path -LiteralPath $pathsBefore.VmxPath
$planJson = $null
$planError = $null
try { $planJson = & $createVmPath -PlanOnly } catch { $planError = $_ }
if ($null -ne $planError -or $LASTEXITCODE -ne 0) {
    $liveCapacity = Test-AimiliHostCapacity -Facts (Get-AimiliHostFacts)
    Assert-True (-not $liveCapacity.Passed) 'PlanOnly failed without reporting a real host-capacity rejection'
} else {
    $safePlan = $planJson | ConvertFrom-Json
    Assert-Equal 2 $safePlan.vcpus 'PlanOnly CPU value differs from the VM plan'
    Assert-Equal 2048 $safePlan.memoryMiB 'PlanOnly memory value differs from the VM plan'
    Assert-True $safePlan.bridgeReady 'PlanOnly did not confirm the physical bridge'
    Assert-Equal 'physical-bridge' $safePlan.networkMode 'PlanOnly reported the wrong network mode'
    Assert-True ($null -eq $safePlan.hostOnlyReady) 'PlanOnly retained the abandoned host-only network'
    Assert-Equal $vmxExistedBefore (Test-Path -LiteralPath $pathsBefore.VmxPath) 'PlanOnly created or removed a VMX file'
}

$installerFixture = Join-Path $PSScriptRoot 'native-installers-fixture.tests.sh'
if (Test-Path -LiteralPath $installerFixture -PathType Leaf) {
    Push-Location (Resolve-Path (Join-Path $PSScriptRoot '..\..\..'))
    try { & wsl.exe -u root -- bash 'deploy/local-vm/tests/native-installers-fixture.tests.sh' } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw "native installer fixture failed with exit code $LASTEXITCODE" }
}
$hardeningFixture = Join-Path $PSScriptRoot 'native-installer-hardening.tests.sh'
if (Test-Path -LiteralPath $hardeningFixture -PathType Leaf) {
    Push-Location (Resolve-Path (Join-Path $PSScriptRoot '..\..\..'))
    try { & wsl.exe -u root -- bash 'deploy/local-vm/tests/native-installer-hardening.tests.sh' } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw "native installer hardening fixture failed with exit code $LASTEXITCODE" }
}
$orchestrationFixture = Join-Path $PSScriptRoot 'native-orchestration-fixture.tests.sh'
if (Test-Path -LiteralPath $orchestrationFixture -PathType Leaf) {
    Push-Location (Resolve-Path (Join-Path $PSScriptRoot '..\..\..'))
    try { & wsl.exe -u root -- bash 'deploy/local-vm/tests/native-orchestration-fixture.tests.sh' } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw "native orchestration fixture failed with exit code $LASTEXITCODE" }
}
Write-Output 'PASS local VM tests'
