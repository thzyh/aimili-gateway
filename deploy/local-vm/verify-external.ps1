[CmdletBinding()]
param(
    [string]$StatePath = (Join-Path (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AimiliGateway\vmware-local') 'state.json'),
    [string]$RuntimeRoot = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AimiliGateway\vmware-local'),
    [string]$ManifestPath = (Join-Path $PSScriptRoot 'native\deployment.json'),
    [string]$PythonCommand = 'python',
    [string]$VerifierPath = (Join-Path $PSScriptRoot '..\..\scripts\verify-external-client-v1c.py'),
    [string]$XrayPath = 'E:\SoftWare\v2rayN-windows-64\bin\xray\xray.exe',
    [string]$EvidencePath,
    [switch]$AsJson
)

$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSScriptRoot 'lib\AimiliLocalVm.psm1') -Force
foreach ($path in @($StatePath,$ManifestPath,$VerifierPath,$XrayPath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "external_verification_input_missing:$path" }
}
$keyPath = Join-Path $RuntimeRoot 'id_ed25519'
$knownHosts = Join-Path $RuntimeRoot 'known_hosts'
foreach ($path in @($keyPath,$knownHosts)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "external_verification_ssh_material_missing:$path" }
}
$state = Get-Content -LiteralPath $StatePath -Raw | ConvertFrom-Json
$manifest = Get-Content -LiteralPath $ManifestPath -Raw | ConvertFrom-Json
$guestAddress = [string]$state.guestAddress
$parsedAddress = [Net.IPAddress]::Parse($guestAddress)
$bytes = $parsedAddress.GetAddressBytes()
$privateAddress = $parsedAddress.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork -and (
    $bytes[0] -eq 10 -or ($bytes[0] -eq 172 -and $bytes[1] -ge 16 -and $bytes[1] -le 31) -or ($bytes[0] -eq 192 -and $bytes[1] -eq 168)
)
if (-not $privateAddress) { throw 'external_verification_guest_address_invalid' }
$expectedGroups = 1 + [int]$manifest.expected.exitSlots
if ($expectedGroups -lt 1) { throw 'external_verification_manifest_invalid' }
if (-not $EvidencePath) { $EvidencePath = Join-Path $RuntimeRoot 'verification\external-client.json' }

$beforeSafety = Get-AimiliHostSafetySnapshot
$arguments = @(
    $VerifierPath,
    '--ssh-target', "aimili@$guestAddress",
    '--ssh-bind', [string]$state.allowedSource,
    '--ssh-key', $keyPath,
    '--known-hosts', $knownHosts,
    '--xray', $XrayPath,
    '--probe-public-socks'
)
$output = @(& $PythonCommand @arguments)
$exitCode = $LASTEXITCODE
$afterSafety = Get-AimiliHostSafetySnapshot
Assert-AimiliHostSafetyUnchanged -Before $beforeSafety -After $afterSafety
$lines = @($output | Where-Object { -not [string]::IsNullOrWhiteSpace([string]$_) })
$result = $null
if ($lines.Count -eq 1) {
    try { $result = $lines[0] | ConvertFrom-Json -ErrorAction Stop } catch { $result = $null }
}
$valid = $exitCode -eq 0 -and $null -ne $result -and $result.status -eq 'pass' -and
    [int]$result.ready_groups -eq $expectedGroups -and [int]$result.verified_groups -eq $expectedGroups -and
    $result.unique_exit_ips -eq $true -and $result.all_public_socks5h -eq $true -and
    $result.all_authorized_socks5h -eq $true -and $result.all_external_public_protocol -eq $true
if ($valid) {
    foreach ($name in @('ready_groups','mixed_count','public_count','single_xray','subscription_entries')) {
        if (-not $result.invariants.PSObject.Properties[$name] -or $result.invariants.$name -ne $true) { $valid = $false }
    }
}
$evidence = [ordered]@{
    schemaVersion = 1
    capturedAt = [DateTimeOffset]::UtcNow.ToString('o')
    guestAddress = $guestAddress
    expectedGroups = $expectedGroups
    hostSafetyUnchanged = $true
    result = $result
}
New-Item -ItemType Directory -Path (Split-Path $EvidencePath) -Force | Out-Null
$evidence | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $EvidencePath -Encoding utf8NoBOM
if (-not $valid) {
    $category = if ($result -and $result.PSObject.Properties['error_category']) { [string]$result.error_category } else { 'invalid_result' }
    throw "external_data_plane_failed:$category"
}
if ($AsJson) { $result | ConvertTo-Json -Depth 10 -Compress } else { $result }
