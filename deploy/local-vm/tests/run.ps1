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

Write-Output 'PASS local VM tests'
