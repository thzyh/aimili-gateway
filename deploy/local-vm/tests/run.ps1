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

$good = [pscustomobject]@{
    LogicalProcessors = 12
    FreeMemoryGiB = 3.85
    DFreeGiB = 129
    HypervisorPresent = $true
    VmwareRoot = 'E:\SoftWare\Vmware16'
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
    RunningVmCount = 0
}
$lowResult = Test-AimiliHostCapacity -Facts $lowMemory
Assert-True (-not $lowResult.Passed) 'host below the memory floor was accepted'
Assert-True (@($lowResult.Reasons) -contains 'free_memory_below_3_5_gib') 'low memory reason was not reported'

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

Write-Output 'PASS local VM tests'
