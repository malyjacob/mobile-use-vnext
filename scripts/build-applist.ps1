[CmdletBinding()]
param(
    [string]$AndroidJar = "",
    [string]$OutputDex = ""
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repository = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$sourceRoot = Join-Path $repository "third_party/android-applist"
if (-not $OutputDex) {
    $OutputDex = Join-Path $sourceRoot "applist.dex"
}

function Get-JavaExecutable([string]$Name) {
    if ($env:JAVA_HOME) {
        $candidate = Join-Path $env:JAVA_HOME "bin/$Name.exe"
        if (Test-Path -LiteralPath $candidate) {
            return $candidate
        }
    }
    $command = Get-Command $Name -ErrorAction Stop
    return $command.Source
}

function Invoke-Checked([string]$Description, [scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

$javac = Get-JavaExecutable "javac"
$sdkRoot = $env:ANDROID_HOME
if (-not $sdkRoot) {
    $sdkRoot = $env:ANDROID_SDK_ROOT
}
if (-not $sdkRoot) {
    throw "ANDROID_HOME/ANDROID_SDK_ROOT is required"
}

if (-not $AndroidJar) {
    $platform = Get-ChildItem -LiteralPath (Join-Path $sdkRoot "platforms") -Directory |
        Sort-Object Name -Descending |
        Select-Object -First 1
    if ($null -eq $platform) {
        throw "No Android platform found under $sdkRoot/platforms"
    }
    $AndroidJar = Join-Path $platform.FullName "android.jar"
}
$androidJarPath = (Resolve-Path $AndroidJar).Path

$d8 = Join-Path $sdkRoot "build-tools"
$d8 = Get-ChildItem -LiteralPath $d8 -Directory |
    Sort-Object Name -Descending |
    ForEach-Object { Join-Path $_.FullName "d8.bat" } |
    Where-Object { Test-Path -LiteralPath $_ } |
    Select-Object -First 1
if (-not $d8) {
    throw "d8.bat not found under $sdkRoot/build-tools"
}

$buildRoot = Join-Path $sourceRoot ".build"
if (Test-Path -LiteralPath $buildRoot) {
    $resolvedBuild = (Resolve-Path $buildRoot).Path
    if (-not $resolvedBuild.StartsWith($sourceRoot + [IO.Path]::DirectorySeparatorChar)) {
        throw "Refusing to remove build directory outside third_party: $resolvedBuild"
    }
    Remove-Item -LiteralPath $resolvedBuild -Recurse -Force
}
$classRoot = Join-Path $buildRoot "classes"
$dexRoot = Join-Path $buildRoot "dex"
New-Item -ItemType Directory -Path $classRoot, $dexRoot -Force | Out-Null

$source = Join-Path $sourceRoot "src/com/mobileuse/applist/AppList.java"
Invoke-Checked "AppList compilation" {
    & $javac -Xlint:-options -source 8 -target 8 -encoding UTF-8 `
        -cp $androidJarPath -d $classRoot $source
}
$classFile = Join-Path $classRoot "com/mobileuse/applist/AppList.class"
Invoke-Checked "AppList dex conversion" {
    & $d8 --lib $androidJarPath --min-api 21 --output $dexRoot $classFile
}

$dex = Join-Path $dexRoot "classes.dex"
$outputPath = [System.IO.Path]::GetFullPath($OutputDex)
New-Item -ItemType Directory -Path (Split-Path -Parent $outputPath) -Force | Out-Null
Copy-Item -LiteralPath $dex -Destination $outputPath -Force

$outputHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $outputPath).Hash.ToLowerInvariant()
Write-Host "Output DEX SHA-256: $outputHash"
Write-Host "Output DEX: $outputPath"
