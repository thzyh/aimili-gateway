[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$path = Join-Path $PSScriptRoot '..\native\install-aimilivpn.sh'
if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw 'AimiliVPN native installer is missing' }
$source = Get-Content -LiteralPath $path -Raw
foreach ($token in @('--check', '--apply', '--installer', 'local_installer', 'MULTI_EXIT_SLOTS', 'MAX_EXIT_SLOTS', 'source-commit', 'aimilivpn.service')) {
    if ($source -notmatch [regex]::Escape($token)) { throw "AimiliVPN installer contract missing: $token" }
}
if ($source -match 'docker|ssh ny|v2rayN') { throw 'AimiliVPN installer contains an out-of-scope integration' }
if ($source -match 'for command in[^\r\n]*openvpn') { throw 'AimiliVPN installer requires OpenVPN before the installer can install it' }
if ($source -notmatch 'openvpn_present=') { throw 'AimiliVPN installer does not report the pre-install OpenVPN state' }
if ($source -notmatch 'command -v openvpn[^\r\n]+openvpn_missing_after_install') { throw 'AimiliVPN installer does not verify OpenVPN after installation' }
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..\..\..')
Push-Location $repoRoot
try {
    & wsl.exe -u root -- bash 'deploy/local-vm/tests/aimilivpn-installer-fixture.tests.sh'
    if ($LASTEXITCODE -ne 0) { throw "AimiliVPN installer fixture failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}
Write-Output 'PASS AimiliVPN native installer contract'
