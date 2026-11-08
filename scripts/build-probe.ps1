param(
    [string]$SdkRoot = 'D:\Android\Sdk',
    [string]$JdkRoot = 'D:\Android\Jdk\17',
    [string]$Platform = 'android-37.0',
    [string]$BuildToolsVersion = '36.0.0'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# Verification-only target app: native SDK pipeline (aapt2 -> javac -> d8 ->
# zipalign -> apksigner), same toolchain as the control App.
function Invoke-AndroidTool {
    param([string]$Executable, [string[]]$ToolArguments)
    & $Executable @ToolArguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Executable exited with code $LASTEXITCODE"
    }
}

$appDirectory = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../android-control/test-target/app/src/main'))
$buildDirectory = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../android-control/build-probe'))
$workDirectory = [System.IO.Path]::GetFullPath((Join-Path $buildDirectory 'work'))
$signingDirectory = Join-Path $buildDirectory 'signing'
$classesDirectory = Join-Path $workDirectory 'classes'
$generatedDirectory = Join-Path $workDirectory 'generated'
$dexDirectory = Join-Path $workDirectory 'dex'
$androidJar = Join-Path $SdkRoot "platforms/$Platform/android.jar"
$buildTools = Join-Path $SdkRoot "build-tools/$BuildToolsVersion"
$javaBin = Join-Path $JdkRoot 'bin'
$apk = Join-Path $buildDirectory 'probe.apk'

foreach ($required in @($androidJar, (Join-Path $javaBin 'javac.exe'),
        (Join-Path $javaBin 'jar.exe'), (Join-Path $javaBin 'keytool.exe'),
        (Join-Path $buildTools 'aapt2.exe'), (Join-Path $buildTools 'd8.bat'),
        (Join-Path $buildTools 'zipalign.exe'), (Join-Path $buildTools 'apksigner.bat'))) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Required SDK/JDK file is missing: $required"
    }
}

if (Test-Path -LiteralPath $workDirectory) {
    Remove-Item -LiteralPath $workDirectory -Recurse -Force
}
if (Test-Path -LiteralPath $apk) {
    Remove-Item -LiteralPath $apk -Force
}
New-Item -ItemType Directory -Path $classesDirectory, $generatedDirectory,
    $dexDirectory, $signingDirectory -Force | Out-Null

$previousJavaHome = $env:JAVA_HOME
try {
    $env:JAVA_HOME = $JdkRoot
    $resources = Join-Path $workDirectory 'resources.zip'
    Invoke-AndroidTool (Join-Path $buildTools 'aapt2.exe') @(
        'compile', '--dir', (Join-Path $appDirectory 'res'), '-o', $resources
    )
    $baseApk = Join-Path $workDirectory 'base.apk'
    Invoke-AndroidTool (Join-Path $buildTools 'aapt2.exe') @(
        'link', '-o', $baseApk, '-I', $androidJar,
        '--manifest', (Join-Path $appDirectory 'AndroidManifest.xml'),
        '--java', $generatedDirectory, $resources
    )
    $sourceFiles = @(Get-ChildItem -LiteralPath (Join-Path $appDirectory 'java'),
        $generatedDirectory -Filter '*.java' -Recurse | Select-Object -ExpandProperty FullName)
    Invoke-AndroidTool (Join-Path $javaBin 'javac.exe') (
        @('--release', '8', '-encoding', 'UTF-8', '-classpath', $androidJar,
            '-d', $classesDirectory) + $sourceFiles
    )
    $classesJar = Join-Path $workDirectory 'classes.jar'
    Invoke-AndroidTool (Join-Path $javaBin 'jar.exe') @(
        '--create', '--file', $classesJar, '--no-manifest', '-C', $classesDirectory, '.'
    )
    Invoke-AndroidTool (Join-Path $buildTools 'd8.bat') @(
        '--lib', $androidJar, '--min-api', '36', '--output', $dexDirectory, $classesJar
    )
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::Open($baseApk, 'Update')
    try {
        [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
            $archive, (Join-Path $dexDirectory 'classes.dex'), 'classes.dex'
        ) | Out-Null
    } finally {
        $archive.Dispose()
    }
    $alignedApk = Join-Path $workDirectory 'aligned.apk'
    Invoke-AndroidTool (Join-Path $buildTools 'zipalign.exe') @('-f', '4', $baseApk, $alignedApk)

    $keystore = Join-Path $signingDirectory 'probe.keystore'
    if (-not (Test-Path -LiteralPath $keystore)) {
        Invoke-AndroidTool (Join-Path $javaBin 'keytool.exe') @(
            '-genkeypair', '-keystore', $keystore, '-alias', 'probe-debug',
            '-storepass', 'android', '-keypass', 'android', '-keyalg', 'RSA',
            '-keysize', '2048', '-validity', '3650', '-dname', 'CN=Mobile Use Probe Development'
        )
    }
    Invoke-AndroidTool (Join-Path $buildTools 'apksigner.bat') @(
        'sign', '--ks', $keystore, '--ks-key-alias', 'probe-debug',
        '--ks-pass', 'pass:android', '--key-pass', 'pass:android', '--out', $apk, $alignedApk
    )
    Invoke-AndroidTool (Join-Path $buildTools 'zipalign.exe') @('-c', '4', $apk)
    Write-Output "Probe APK: $apk"
} finally {
    $env:JAVA_HOME = $previousJavaHome
}
