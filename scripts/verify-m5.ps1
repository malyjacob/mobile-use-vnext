#Requires -Version 5.1
<#
.SYNOPSIS
    Verifies the MCP adapter entries of the deployed device service.

.DESCRIPTION
    Reads .state/deploy.json, refreshes the private MCP configuration, builds
    the host binaries, runs the MCP probe for the requested transports and
    prints the service health/status summary. Tokens, node trees, image data,
    shell output and notification text are never printed.

    The service must already be deployed by scripts/deploy.ps1 and the control
    App must report can_start=true. This script never deploys, restarts or
    stops the service.

.PARAMETER HostPort
    Service host port. Defaults to host_port in .state/deploy.json.

.PARAMETER SkipBuild
    Reuse the existing build/device-mcp.exe and build/mcp-probe.exe.

.PARAMETER Transport
    All, Stdio, Http or PRecovery.

.NOTES
    Exit codes: 0 = PASS, 1 = FAIL, 2 = BLOCKED (not admissible, or the HTTP
    listener is already occupied).
#>
param(
    [int]$HostPort = 0,
    [switch]$SkipBuild,
    [ValidateSet('All', 'Stdio', 'Http', 'PRecovery')]
    [string]$Transport = 'All'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$stateDirectory = Join-Path $repository '.state'
$deployStatePath = Join-Path $stateDirectory 'deploy.json'
$mcpConfigPath = Join-Path $stateDirectory 'mcp-config.json'
$adapterBinary = Join-Path $repository 'build/device-mcp.exe'
$probeBinary = Join-Path $repository 'build/mcp-probe.exe'
$httpListen = '127.0.0.1:18882'
$httpPort = 18882
$allowedOrigin = 'http://127.0.0.1:3000'
$probeTimeoutSeconds = 180
$contractVersion = '1'

function Write-Utf8NoBom {
    param([string]$Path, [string]$Text)
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Text, $encoding)
}

function Get-Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return $null }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function New-HexToken {
    $bytes = New-Object byte[] 32
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
}

try {
    if (-not (Test-Path -LiteralPath $deployStatePath -PathType Leaf)) {
        throw 'deploy_state_missing'
    }
    try {
        $deploy = Get-Content -LiteralPath $deployStatePath -Raw | ConvertFrom-Json
    } catch {
        throw 'deploy_state_invalid'
    }

    $agentToken = [string](Get-Field $deploy 'agent_token')
    if ($agentToken -eq '') { throw 'deploy_state_agent_token_missing' }

    if ($HostPort -gt 0) {
        $servicePort = $HostPort
    } else {
        $deployPort = Get-Field $deploy 'host_port'
        if ($null -eq $deployPort) { throw 'deploy_state_host_port_missing' }
        $servicePort = [int]$deployPort
    }
    if ($servicePort -lt 1 -or $servicePort -gt 65535) { throw 'host_port_invalid' }
    $baseUrl = 'http://127.0.0.1:' + $servicePort

    $mcpToken = New-HexToken
    while ($mcpToken -eq $agentToken) {
        $mcpToken = New-HexToken
    }

    $mcpConfig = [ordered]@{
        service = [ordered]@{
            base_url    = $baseUrl
            agent_token = $agentToken
        }
        transport = 'stdio'
        http      = [ordered]@{
            listen          = $httpListen
            token           = $mcpToken
            allowed_origins = @($allowedOrigin)
        }
    }
    Write-Utf8NoBom -Path $mcpConfigPath -Text ($mcpConfig | ConvertTo-Json -Depth 5)
    $null = & icacls $mcpConfigPath /inheritance:r /grant:r "$($env:USERNAME):(R,W)" 2>&1
    if ($LASTEXITCODE -ne 0) { throw 'mcp_config_acl_failed' }
    Write-Output 'config refreshed path=.state/mcp-config.json permissions=owner-only'

    if (-not $SkipBuild) {
        $previousExperiment = $env:GOEXPERIMENT
        try {
            $env:GOEXPERIMENT = 'jsonv2'
            Push-Location $repository
            try {
                & go build -o $adapterBinary ./cmd/device-mcp
                if ($LASTEXITCODE -ne 0) { throw 'adapter_build_failed' }
                & go build -o $probeBinary ./scripts/mcp-probe
                if ($LASTEXITCODE -ne 0) { throw 'probe_build_failed' }
            } finally {
                Pop-Location
            }
        } finally {
            if ($null -eq $previousExperiment) {
                Remove-Item Env:GOEXPERIMENT -ErrorAction SilentlyContinue
            } else {
                $env:GOEXPERIMENT = $previousExperiment
            }
        }
        Write-Output 'build adapter=build/device-mcp.exe probe=build/mcp-probe.exe'
    } else {
        foreach ($required in @($adapterBinary, $probeBinary)) {
            if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
                throw 'binary_missing_build_required'
            }
        }
        Write-Output 'build skipped'
    }

    $modes = @()
    switch ($Transport) {
        'All' { $modes = @('Stdio', 'Http', 'PRecovery') }
        'Stdio' { $modes = @('Stdio') }
        'Http' { $modes = @('Http') }
        'PRecovery' { $modes = @('PRecovery') }
    }

    if ($modes -contains 'Http') {
        $listener = $null
        $bound = $false
        try {
            $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $httpPort)
            $listener.Start()
            $bound = $true
        } catch {
            Write-Output ('BLOCKED http reason=listener_occupied listen=' + $httpListen)
            Write-Output 'RESULT BLOCKED'
            exit 2
        } finally {
            if ($bound -and $null -ne $listener) { $listener.Stop() }
        }
    }

    $probeTransportByMode = @{
        Stdio     = 'stdio'
        Http      = 'http'
        PRecovery = 'p-recovery'
    }

    foreach ($mode in $modes) {
        $probeTransport = $probeTransportByMode[$mode]
        Write-Output ('== probe ' + $probeTransport + ' ==')
        $probeArguments = @(
            '-config', $mcpConfigPath,
            '-binary', $adapterBinary,
            '-transport', $probeTransport,
            '-timeout', [string]$probeTimeoutSeconds
        )
        $previousErrorAction = $ErrorActionPreference
        $probeOutput = @()
        $probeExitCode = 1
        try {
            $ErrorActionPreference = 'Continue'
            $probeOutput = & $probeBinary @probeArguments 2>&1
            $probeExitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousErrorAction
        }
        foreach ($line in $probeOutput) {
            Write-Output ([string]$line)
        }
        Write-Output ('probe ' + $probeTransport + ' exit=' + $probeExitCode)
        if ($probeExitCode -ne 0) {
            if ($probeExitCode -eq 2) {
                Write-Output 'RESULT BLOCKED'
                exit 2
            }
            Write-Output 'RESULT FAIL'
            exit 1
        }
    }

    $health = $null
    try {
        $health = Invoke-RestMethod -Method Get -Uri ($baseUrl + '/health') -TimeoutSec 10
    } catch {
        Write-Output 'service health=unreachable'
        Write-Output 'RESULT FAIL'
        exit 1
    }
    $healthStatus = [string](Get-Field $health 'status')
    $contract = [string](Get-Field $health 'contract_version')
    Write-Output ('service health=' + $healthStatus + ' contract_version=' + $contract)

    $status = $null
    try {
        $headers = @{
            Authorization           = 'Bearer ' + $agentToken
            'X-Mobile-Use-Contract' = $contractVersion
        }
        $status = Invoke-RestMethod -Method Post -Uri ($baseUrl + '/api/status') `
            -Headers $headers -ContentType 'application/json' -Body '{}' -TimeoutSec 15
    } catch {
        Write-Output 'service status=unreachable'
        Write-Output 'RESULT FAIL'
        exit 1
    }
    $canStart = [bool](Get-Field $status 'can_start')
    $blockingCode = [string](Get-Field $status 'blocking_code')
    if ($canStart) {
        Write-Output 'service can_start=true'
        Write-Output 'RESULT PASS'
        exit 0
    }
    Write-Output 'service can_start=false'
    if ($blockingCode -ne '') {
        Write-Output ('service blocking_code=' + $blockingCode)
    }
    Write-Output 'RESULT BLOCKED'
    exit 2
} catch {
    $reason = $_.Exception.Message
    if ($reason -eq '') { $reason = 'unexpected_error' }
    Write-Output ('FAIL run reason=' + $reason)
    Write-Output 'RESULT FAIL'
    exit 1
}
