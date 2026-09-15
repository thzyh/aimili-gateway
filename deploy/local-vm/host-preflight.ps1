[CmdletBinding()]
param([switch]$AsJson)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force

$facts = Get-AimiliHostFacts
$capacity = Test-AimiliHostCapacity -Facts $facts
$report = [pscustomobject]@{
    passed = [bool]$capacity.Passed
    reasons = @($capacity.Reasons)
    host = [pscustomobject]@{
        physicalCores = $facts.PhysicalCores
        logicalProcessors = $facts.LogicalProcessors
        totalMemoryGiB = $facts.TotalMemoryGiB
        freeMemoryGiB = $facts.FreeMemoryGiB
        dDriveFreeGiB = $facts.DFreeGiB
        hypervisorPresent = $facts.HypervisorPresent
        vmwareAvailable = (Test-Path -LiteralPath $facts.VmwareRoot -PathType Container)
        runningVmCount = $facts.RunningVmCount
    }
    target = [pscustomobject]@{
        vcpus = 2
        memoryMiB = 2048
        diskGiB = 24
        swapGiB = 1
    }
}

if ($AsJson) {
    $report | ConvertTo-Json -Depth 4 -Compress
} else {
    $report
}

if (-not $capacity.Passed) { exit 2 }
