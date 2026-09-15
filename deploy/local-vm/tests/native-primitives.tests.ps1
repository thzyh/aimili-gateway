[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$root = Join-Path $PSScriptRoot '..\native'
$files = @('stage.sh', 'backup.sh', 'rollback.sh')
foreach ($name in $files) {
    $path = Join-Path $root $name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "native primitive missing: $name" }
    $source = Get-Content -LiteralPath $path -Raw
    if ($source -match 'rm\s+-rf\s+[/"'']|/var/lib/aimilivpn') { throw "native primitive contains an unsafe broad target: $name" }
    if ($source -notmatch 'umask\s+077') { throw "native primitive does not set a restrictive umask: $name" }
}
Write-Output 'PASS native primitive contract'
