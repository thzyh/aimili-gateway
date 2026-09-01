[CmdletBinding()]
param(
    [string]$AimiliVPNRepository = 'D:\CodexProject\Github\aimili-vpngate\.worktrees\main-switch-protocol-modes',
    [string]$XUIDeployRepository = 'D:\CodexProject\Github\aimili-3xui-deploy\.worktrees\main-switch-protocol-modes',
    [string]$XUISourceRepository = 'D:\CodexProject\Github\.tmp\3x-ui-v3.7.0',
    [switch]$IntegrationOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$gatewayRepository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$aimiliRepository = (Resolve-Path -LiteralPath $AimiliVPNRepository).Path
$xuiDeployRepository = (Resolve-Path -LiteralPath $XUIDeployRepository).Path
$xuiSourceRepository = (Resolve-Path -LiteralPath $XUISourceRepository).Path
$expectedXUICommit = 'f727d04f6522bb94a8fb52e8352fdcafb51c11e1'
$patchPath = Join-Path $xuiDeployRepository 'patches\3x-ui-v3.7.0-client-inbound-alias.patch'
$workspaceRoot = [System.IO.Path]::GetFullPath((Join-Path $gatewayRepository '..\..\..'))
$gatewayGoCache = Join-Path $workspaceRoot '.tmp\go-cache'
$xuiGoCache = Join-Path $workspaceRoot '.tmp\go-cache-3xui'

function Invoke-Checked {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory
    )

    Write-Host "VERIFY $Label"
    Push-Location $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Label failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Assert-CleanPinnedXUISource {
    $actualOutput = @(& git -c "safe.directory=$xuiSourceRepository" -C $xuiSourceRepository rev-parse HEAD)
    if ($LASTEXITCODE -ne 0 -or $actualOutput.Count -ne 1) {
        throw '3x-ui source commit could not be read'
    }
    $actual = $actualOutput[0].Trim()
    if ($actual -ne $expectedXUICommit) {
        throw '3x-ui source is not the pinned v3.7.0 commit'
    }
    if (-not (Test-Path -LiteralPath $patchPath -PathType Leaf)) {
        throw '3x-ui alias patch is missing'
    }
}

Assert-CleanPinnedXUISource

$tmpRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("aimili-egress-alias-" + [guid]::NewGuid().ToString('N'))
$patchedXUI = Join-Path $tmpRoot '3x-ui'
$gatewayBinary = Join-Path $tmpRoot 'aimili-gateway.exe'
$adminBinary = Join-Path $tmpRoot 'aimili-gateway-admin.exe'
New-Item -ItemType Directory -Path $tmpRoot | Out-Null

try {
    Invoke-Checked -Label 'AimiliVPN fixture: candidate IP, structured refresh, persisted rejection' `
        -FilePath 'python' `
        -Arguments @('-m', 'unittest',
            'tests.test_candidate_exit_ip',
            'tests.test_pool_maintenance.PoolMaintenanceTests',
            'tests.test_exit_slot_types.ExitSlotTypeTests.test_mark_candidate_unavailable_persists_blacklist_and_pool_state',
            '-v') `
        -WorkingDirectory $aimiliRepository

    Invoke-Checked -Label '3x-ui deployment assets' `
        -FilePath 'powershell.exe' `
        -Arguments @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', '.\tests\run-all.ps1') `
        -WorkingDirectory $xuiDeployRepository

    Invoke-Checked -Label 'clone pinned 3x-ui fixture' `
        -FilePath 'git' `
        -Arguments @('-c', "safe.directory=$xuiSourceRepository", '-c', "safe.directory=$xuiSourceRepository/.git", 'clone', '--quiet', '--no-checkout', '--', $xuiSourceRepository, $patchedXUI) `
        -WorkingDirectory $tmpRoot
    Invoke-Checked -Label 'checkout pinned 3x-ui fixture' `
        -FilePath 'git' `
        -Arguments @('-C', $patchedXUI, 'checkout', '--quiet', '--detach', $expectedXUICommit) `
        -WorkingDirectory $tmpRoot
    Invoke-Checked -Label 'check pinned 3x-ui patch' `
        -FilePath 'git' `
        -Arguments @('-C', $patchedXUI, 'apply', '--check', '--unidiff-zero', '--', $patchPath) `
        -WorkingDirectory $tmpRoot
    Invoke-Checked -Label 'apply pinned 3x-ui patch' `
        -FilePath 'git' `
        -Arguments @('-C', $patchedXUI, 'apply', '--unidiff-zero', '--', $patchPath) `
        -WorkingDirectory $tmpRoot

    $oldGoToolchain = $env:GOTOOLCHAIN
    $oldGoCache = $env:GOCACHE
    $oldXUISource = $env:AIMILI_XUI_SOURCE
    try {
        $env:GOTOOLCHAIN = 'auto'
        $env:GOCACHE = $xuiGoCache
        $env:AIMILI_XUI_SOURCE = $xuiSourceRepository
        Invoke-Checked -Label '3x-ui fixed-patch aliases and client isolation' `
            -FilePath 'go' `
            -Arguments @('test', './internal/database', './internal/web/service', './internal/web/controller', './internal/sub', '-run', 'Alias|ClientInbound', '-count=1') `
            -WorkingDirectory $patchedXUI
        if (-not $IntegrationOnly) {
            Invoke-Checked -Label '3x-ui fixed-patch four packages' `
                -FilePath 'go' `
                -Arguments @('test', './internal/database', './internal/web/service', './internal/web/controller', './internal/sub', '-count=1') `
                -WorkingDirectory $patchedXUI
            Invoke-Checked -Label '3x-ui fixed-patch frontend install' `
                -FilePath 'npm' `
                -Arguments @('ci') `
                -WorkingDirectory (Join-Path $patchedXUI 'frontend')
            Invoke-Checked -Label '3x-ui fixed-patch frontend build' `
                -FilePath 'npm' `
                -Arguments @('run', 'build') `
                -WorkingDirectory (Join-Path $patchedXUI 'frontend')
            Write-Host 'VERIFY 3x-ui fixed-patch full Go suite'
            $xuiFullLog = Join-Path $tmpRoot 'xui-full-go-test.log'
            & python (Join-Path $PSScriptRoot 'run_bounded_command.py') `
                --timeout-seconds 900 `
                --output $xuiFullLog `
                --working-directory $patchedXUI `
                -- go test -p 1 ./... -count=1
            $xuiFullExit = $LASTEXITCODE
            Invoke-Checked -Label '3x-ui Windows baseline classification' `
                -FilePath 'python' `
                -Arguments @((Join-Path $PSScriptRoot 'verify_xui_windows_baseline.py'), '--output', $xuiFullLog, '--exit-code', [string]$xuiFullExit, '--platform', $(if ($env:OS -eq 'Windows_NT') { 'windows' } else { 'other' })) `
                -WorkingDirectory $gatewayRepository
            Invoke-Checked -Label '3x-ui fixed-patch build' `
                -FilePath 'go' `
                -Arguments @('build', './...') `
                -WorkingDirectory $patchedXUI
        }
    }
    finally {
        $env:GOTOOLCHAIN = $oldGoToolchain
        $env:GOCACHE = $oldGoCache
        $env:AIMILI_XUI_SOURCE = $oldXUISource
    }

    $oldGatewayCache = $env:GOCACHE
    try {
        $env:GOCACHE = $gatewayGoCache
        Invoke-Checked -Label 'Gateway three cross-repository transactions' `
            -FilePath 'go' `
            -Arguments @('test', './internal/orchestrator', '-run', 'SwitchMainProtocolSynchronizesHealthyRuntimeIdentityBeforeStrictPreflight|ReplaceCandidateUpdatesOnlyTheTargetSubscriptionAlias|ReplaceCandidateAliasFailuresRestoreOldState|ReplaceCandidateReloadsCandidatesOnlyAfterPersistedRejection', '-count=1', '-v') `
            -WorkingDirectory $gatewayRepository
        if (-not $IntegrationOnly) {
            Invoke-Checked -Label 'Gateway web tests' -FilePath 'npm' -Arguments @('test', '--prefix', 'web') -WorkingDirectory $gatewayRepository
            Invoke-Checked -Label 'Gateway web build' -FilePath 'npm' -Arguments @('run', 'build', '--prefix', 'web') -WorkingDirectory $gatewayRepository
            Invoke-Checked -Label 'Gateway Go race tests' -FilePath 'go' -Arguments @('test', './...', '-race', '-count=1') -WorkingDirectory $gatewayRepository
            Invoke-Checked -Label 'Gateway Go vet' -FilePath 'go' -Arguments @('vet', './...') -WorkingDirectory $gatewayRepository
            Invoke-Checked -Label 'Gateway service build' -FilePath 'go' -Arguments @('build', '-buildvcs=false', '-o', $gatewayBinary, './cmd/aimili-gateway') -WorkingDirectory $gatewayRepository
            Invoke-Checked -Label 'Gateway admin build' -FilePath 'go' -Arguments @('build', '-buildvcs=false', '-o', $adminBinary, './cmd/aimili-gateway-admin') -WorkingDirectory $gatewayRepository
        }
    }
    finally {
        $env:GOCACHE = $oldGatewayCache
    }

    if (-not $IntegrationOnly) {
        Invoke-Checked -Label 'AimiliVPN full unittest' -FilePath 'python' -Arguments @('-m', 'unittest', 'discover', '-s', 'tests', '-v') -WorkingDirectory $aimiliRepository
        Invoke-Checked -Label 'AimiliVPN py_compile' -FilePath 'python' -Arguments @('-m', 'py_compile', 'proxy_server.py', 'node_pool.py', 'vpngate_manager.py', 'control_api.py', 'vpn_utils.py') -WorkingDirectory $aimiliRepository
        Invoke-Checked -Label 'Gateway diff check' -FilePath 'git' -Arguments @('-c', "safe.directory=$gatewayRepository", '-C', $gatewayRepository, 'diff', '--check') -WorkingDirectory $workspaceRoot
        Invoke-Checked -Label 'AimiliVPN diff check' -FilePath 'git' -Arguments @('-c', "safe.directory=$aimiliRepository", '-C', $aimiliRepository, 'diff', '--check') -WorkingDirectory $workspaceRoot
        Invoke-Checked -Label '3x-ui deploy diff check' -FilePath 'git' -Arguments @('-c', "safe.directory=$xuiDeployRepository", '-C', $xuiDeployRepository, 'diff', '--check') -WorkingDirectory $workspaceRoot
    }

    Write-Host 'VERIFY temporary AimiliVPN, 3x-ui, and Gateway HTTP processes'
    $fixtureOutput = @(& python (Join-Path $PSScriptRoot 'egress_ux_alias_process_fixture.py'))
    if ($LASTEXITCODE -ne 0 -or $fixtureOutput.Count -ne 1) {
        throw 'three-process integration fixture failed'
    }
    $fixture = ($fixtureOutput[0] | ConvertFrom-Json)
    foreach ($transaction in $fixture.transactions) {
        Write-Host ("COVERED SCENARIO scenario={0} role={1} country-code={2} result={3}" -f `
            $transaction.scenario, $transaction.role, $transaction.countryCode, $transaction.result)
    }
    Write-Host 'SAFE OUTPUT: no subscription URL, auth value, UUID, or node configuration was emitted.'
}
finally {
    if (Test-Path -LiteralPath $tmpRoot) {
        Remove-Item -LiteralPath $tmpRoot -Recurse -Force
    }
}
