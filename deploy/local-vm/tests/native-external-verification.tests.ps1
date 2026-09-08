[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$entry = Join-Path $PSScriptRoot '..\verify-external.ps1'
$fixture = Join-Path ([IO.Path]::GetTempPath()) ("aimili-external-verify-{0}" -f [guid]::NewGuid().ToString('N'))
$runtime = Join-Path $fixture 'runtime'
$state = Join-Path $fixture 'state.json'
$manifest = Join-Path $fixture 'deployment.json'
$xray = Join-Path $fixture 'xray.exe'
$verifier = Join-Path $fixture 'verifier.py'
$fakePython = Join-Path $fixture 'python.ps1'
$argumentLog = Join-Path $fixture 'arguments.json'
try {
    New-Item -ItemType Directory -Path $runtime -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $runtime 'id_ed25519') -Value 'fixture-key' -Encoding ascii
    Set-Content -LiteralPath (Join-Path $runtime 'known_hosts') -Value 'fixture-host' -Encoding ascii
    Set-Content -LiteralPath $xray -Value 'fixture-xray' -Encoding ascii
    Set-Content -LiteralPath $verifier -Value 'fixture-verifier' -Encoding ascii
    @{ guestAddress = '192.168.88.4'; allowedSource = '192.168.88.1' } | ConvertTo-Json | Set-Content -LiteralPath $state -Encoding utf8NoBOM
    @{ schemaVersion = 1; expected = @{ exitSlots = 3 } } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $manifest -Encoding utf8NoBOM
    @'
@($args) | ConvertTo-Json | Set-Content -LiteralPath $env:AIMILI_EXTERNAL_ARGUMENT_LOG -Encoding utf8NoBOM
Write-Output '{"status":"pass","ready_groups":4,"verified_groups":4,"source_restriction_enabled":true,"unique_exit_ips":true,"all_public_socks5h":true,"all_authorized_socks5h":true,"all_external_public_protocol":true,"protocol_counts":{"vless":3,"hysteria2":1},"switch_requested":false,"invariants":{"ready_groups":true,"mixed_count":true,"public_count":true,"single_xray":true,"subscription_entries":true},"groups":[]}'
$global:LASTEXITCODE = 0
'@ | Set-Content -LiteralPath $fakePython -Encoding utf8NoBOM
    $env:AIMILI_EXTERNAL_ARGUMENT_LOG = $argumentLog

    $report = & $entry -StatePath $state -RuntimeRoot $runtime -ManifestPath $manifest -PythonCommand $fakePython -VerifierPath $verifier -XrayPath $xray
    if ($report.status -ne 'pass' -or [int]$report.ready_groups -ne 4) { throw 'external verifier entry rejected complete dynamic evidence' }
    $arguments = @((Get-Content -LiteralPath $argumentLog -Raw | ConvertFrom-Json))
    $joined = $arguments -join ' '
    foreach ($expected in @('--ssh-target','aimili@192.168.88.4','--ssh-bind','192.168.88.1','--ssh-key','--known-hosts','--probe-public-socks','--xray')) {
        if ($joined -notmatch [regex]::Escape($expected)) { throw "external verifier invocation omitted: $expected" }
    }
    if ($joined -match '(^|\s)ny($|\s)') { throw 'local VM external verifier used the production SSH alias' }
    $evidence = Join-Path $runtime 'verification\external-client.json'
    if (-not (Test-Path -LiteralPath $evidence -PathType Leaf)) { throw 'external verifier did not persist safe evidence' }

    (Get-Content -LiteralPath $fakePython -Raw).Replace('"ready_groups":4', '"ready_groups":3') | Set-Content -LiteralPath $fakePython -Encoding utf8NoBOM
    try {
        & $entry -StatePath $state -RuntimeRoot $runtime -ManifestPath $manifest -PythonCommand $fakePython -VerifierPath $verifier -XrayPath $xray | Out-Null
        throw 'external verifier accepted incomplete group coverage'
    } catch {
        if ($_.Exception.Message -notmatch 'external_data_plane_failed') { throw }
    }
} finally {
    Remove-Item Env:AIMILI_EXTERNAL_ARGUMENT_LOG -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture -Recurse -Force }
}
$global:LASTEXITCODE = 0
Write-Output 'PASS native external verification entry'
