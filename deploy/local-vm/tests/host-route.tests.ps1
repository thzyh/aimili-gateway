[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\repair-host-route.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    throw 'host route repair script is missing'
}

$source = Get-Content -LiteralPath $scriptPath -Raw
foreach ($required in @(
    'Get-AimiliHostSafetySnapshot',
    'Assert-AimiliHostSafetyUnchanged',
    'Find-NetRoute',
    'New-NetRoute',
    'PersistentStore',
    'route.exe',
    "'-p'",
    'Set-NetIPInterface',
    'target_adapter_has_default_route',
    'host_route_repair_requires_administrator',
    'host_route_not_selected'
)) {
    if ($source -notmatch [regex]::Escape($required)) {
        throw "host route repair omits required guard: $required"
    }
}
if ($source -notmatch 'persistentRequired') {
    throw 'host route repair does not report the persistence requirement'
}

if ($source -match 'Remove-NetRoute|rasdial(?:\.exe)?\s+[^\r\n]*\/disconnect|Set-DnsClient|Set-ItemProperty[^\r\n]*Internet Settings') {
    throw 'host route repair contains an out-of-scope network mutation'
}

$tokens = $null
$parseErrors = $null
[void][System.Management.Automation.Language.Parser]::ParseFile(
    (Resolve-Path $scriptPath),
    [ref]$tokens,
    [ref]$parseErrors
)
if ($parseErrors.Count -gt 0) {
    throw 'host route repair script does not parse'
}

Write-Output 'PASS host route repair contract'
