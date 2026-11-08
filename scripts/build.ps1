param(
    [string]$Output = 'build',
    [string]$Version = '0.3.0'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputDirectory = [System.IO.Path]::GetFullPath((Join-Path $repository $Output))
New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null

$previous = @{
    GOEXPERIMENT = $env:GOEXPERIMENT
    CGO_ENABLED  = $env:CGO_ENABLED
    GOOS         = $env:GOOS
    GOARCH       = $env:GOARCH
}
try {
    $env:GOEXPERIMENT = 'jsonv2'
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = 'arm64'
    Push-Location $repository
    try {
        foreach ($target in @(
                @{ Name = 'device-service'; Package = './cmd/device-service' },
                @{ Name = 'device-mcp'; Package = './cmd/device-mcp' })) {
            $path = Join-Path $outputDirectory $target.Name
            & go build -trimpath -buildvcs=false `
                -ldflags "-s -w -X github.com/malyjacob/mobile-use-vnext/internal/protocol.ReleaseVersion=$Version" `
                -o $path $target.Package
            if ($LASTEXITCODE -ne 0) {
                throw "go build exited with code $LASTEXITCODE for $($target.Package)"
            }
            $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
            Write-Output "$($target.Name): $path"
            Write-Output "  sha256: $hash"
        }
        Write-Output "release version: $Version"
    } finally {
        Pop-Location
    }
} finally {
    foreach ($name in $previous.Keys) {
        if ($null -eq $previous[$name]) {
            Remove-Item "Env:$name" -ErrorAction SilentlyContinue
        } else {
            Set-Item "Env:$name" $previous[$name]
        }
    }
}
