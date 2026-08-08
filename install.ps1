param(
    [string] $Version,
    [string] $Target,
    [string] $InstallDir,
    [string] $ReleaseBaseUrl,
    [string] $Repository
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.Net.Http
$script:VelocityInstallerHttpHandler = $null
. (Join-Path $PSScriptRoot 'scripts/installers/Velocity.Http.ps1')
. (Join-Path $PSScriptRoot 'scripts/installers/Velocity.Archive.ps1')

function Get-HostTarget {
    $architecture = if ($env:PROCESSOR_ARCHITEW6432) {
        $env:PROCESSOR_ARCHITEW6432
    }
    else {
        $env:PROCESSOR_ARCHITECTURE
    }
    switch ($architecture.ToUpperInvariant()) {
        'AMD64' { return 'x86_64-pc-windows-msvc' }
        'ARM64' { return 'aarch64-pc-windows-msvc' }
        default { throw "Unsupported Windows architecture: $architecture" }
    }
}

if ($MyInvocation.InvocationName -eq '.') { return }

if ([string]::IsNullOrWhiteSpace($Version)) {
    $Version = if ($env:VELOCITY_VERSION) { $env:VELOCITY_VERSION } else { 'latest' }
}
if ([string]::IsNullOrWhiteSpace($Repository)) {
    $Repository = if ($env:VELOCITY_REPOSITORY) { $env:VELOCITY_REPOSITORY } else { 'Hunter2030ZeRo/Velocity' }
}
if ($env:OS -ne 'Windows_NT') {
    throw "Unsupported operating system: $([Environment]::OSVersion.Platform)"
}
if ([string]::IsNullOrWhiteSpace($Target)) {
    $Target = if ($env:VELOCITY_TARGET) { $env:VELOCITY_TARGET } else { Get-HostTarget }
}
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = if ($env:VELOCITY_INSTALL_DIR) {
        $env:VELOCITY_INSTALL_DIR
    }
    elseif ($env:LOCALAPPDATA) {
        Join-Path $env:LOCALAPPDATA 'velocity\bin'
    }
    else {
        Join-Path $HOME 'AppData\Local\velocity\bin'
    }
}
if ([string]::IsNullOrWhiteSpace($ReleaseBaseUrl)) {
    $ReleaseBaseUrl = $env:VELOCITY_RELEASE_BASE_URL
}

if ($Version -notmatch '^[A-Za-z0-9._-]+$') { throw "Invalid release version: $Version" }
if ($Repository -notmatch '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$') { throw "Invalid repository: $Repository" }
if ($Target -notin @('x86_64-pc-windows-msvc', 'aarch64-pc-windows-msvc')) {
    throw "Unsupported Windows target: $Target"
}

$InstallDir = [IO.Path]::GetFullPath($InstallDir)
if ($InstallDir -eq [IO.Path]::GetPathRoot($InstallDir)) {
    throw 'Refusing to install directly into a filesystem root'
}
if ([string]::IsNullOrWhiteSpace($ReleaseBaseUrl)) {
    $ReleaseBaseUrl = if ($Version -eq 'latest') {
        "https://github.com/$Repository/releases/latest/download"
    }
    else {
        "https://github.com/$Repository/releases/download/$Version"
    }
}

$assetName = "velocity-$Target.zip"
$temporaryRoot = $null
$publishStage = $null
$publicationLockStream = $null
$publishPending = $false
$previousFiles = @{}
$recoveryMarker = Join-Path $InstallDir '.velocity-recovery-required'
try {
    if (Test-Path -LiteralPath $InstallDir) {
        $installItem = Get-Item -LiteralPath $InstallDir -Force
        if (-not $installItem.PSIsContainer -or ($installItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "Installation directory must be a non-reparse directory: $InstallDir"
        }
    }
    else {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }
    if (Test-Path -LiteralPath $recoveryMarker) {
        throw "A previous interrupted publication requires manual recovery: $recoveryMarker"
    }

    $publicationLock = Join-Path $InstallDir '.velocity-install.lock'
    if (Test-Path -LiteralPath $publicationLock) {
        $lockItem = Get-Item -LiteralPath $publicationLock -Force
        if ($lockItem.PSIsContainer -or ($lockItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "Publication lock path is unsafe: $publicationLock"
        }
    }
    try {
        $publicationLockStream = [IO.File]::Open(
            $publicationLock,
            [IO.FileMode]::OpenOrCreate,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::None
        )
    }
    catch {
        throw "Another installation is publishing: $publicationLock"
    }

    $temporaryRoot = Join-Path $InstallDir (".velocity-download-{0}" -f [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $temporaryRoot | Out-Null
    $archive = Join-Path $temporaryRoot $assetName
    $manifest = Join-Path $temporaryRoot 'SHA256SUMS'
    Copy-ReleaseFile -Base $ReleaseBaseUrl -Name $assetName -Destination $archive
    Copy-ReleaseFile -Base $ReleaseBaseUrl -Name 'SHA256SUMS' -Destination $manifest

    $expectedHash = Get-ExpectedHash -Manifest $manifest -AssetName $assetName
    $actualHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -cne $expectedHash) { throw "Checksum mismatch for $assetName" }

    $extracted = Join-Path $temporaryRoot 'extracted'
    New-Item -ItemType Directory -Path $extracted | Out-Null
    Expand-VelocityArchive -Archive $archive -Destination $extracted

    $publishStage = Join-Path $InstallDir (".velocity-install-{0}" -f [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $publishStage | Out-Null
    foreach ($name in @('velocity.exe', 'velocity-resolver.exe')) {
        $destination = Join-Path $InstallDir $name
        if (Test-Path -LiteralPath $destination) {
            $destinationItem = Get-Item -LiteralPath $destination -Force
            if ($destinationItem.PSIsContainer -or ($destinationItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
                throw "Existing destination must be a regular file: $destination"
            }
            $backup = Join-Path $publishStage (".previous-{0}" -f $name)
            Copy-Item -LiteralPath $destination -Destination $backup
            $previousFiles[$name] = $backup
        }
        Copy-Item -LiteralPath (Join-Path $extracted $name) -Destination (Join-Path $publishStage $name)
    }

    [IO.File]::WriteAllText($recoveryMarker, $publishStage)
    $publishPending = $true
    try {
        Move-Item -LiteralPath (Join-Path $publishStage 'velocity.exe') `
            -Destination (Join-Path $InstallDir 'velocity.exe') -Force
        Move-Item -LiteralPath (Join-Path $publishStage 'velocity-resolver.exe') `
            -Destination (Join-Path $InstallDir 'velocity-resolver.exe') -Force
        Remove-Item -LiteralPath $recoveryMarker -Force
        $publishPending = $false
    }
    catch {
        $publicationError = $_
        try {
            Restore-PublishedPair -PreviousFiles $previousFiles -InstallDir $InstallDir
            Remove-Item -LiteralPath $recoveryMarker -Force
            $publishPending = $false
        }
        catch {
            throw "Publication failed: $($publicationError.Exception.Message); rollback failed: $($_.Exception.Message)"
        }
        throw $publicationError
    }

    Write-Output "Installed Velocity ($Target) to $InstallDir"
    $pathEntries = @($env:PATH -split [IO.Path]::PathSeparator)
    if (-not ($pathEntries -contains $InstallDir)) {
        Write-Warning "Add $InstallDir to PATH to run velocity."
    }
}
finally {
    $rollbackFailure = $null
    try {
        if ($publishPending) {
            Restore-PublishedPair -PreviousFiles $previousFiles -InstallDir $InstallDir
            Remove-Item -LiteralPath $recoveryMarker -Force
            $publishPending = $false
        }
    }
    catch {
        $rollbackFailure = $_
    }
    finally {
        if (-not $publishPending -and $null -ne $publishStage -and (Test-Path -LiteralPath $publishStage)) {
            Remove-Item -LiteralPath $publishStage -Recurse -Force -ErrorAction SilentlyContinue
        }
        if ($null -ne $publicationLockStream) { $publicationLockStream.Dispose() }
        if ($null -ne $temporaryRoot -and (Test-Path -LiteralPath $temporaryRoot)) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    if ($null -ne $rollbackFailure) {
        throw "Rollback failed; recovery files remain at $publishStage and marker $recoveryMarker`: $($rollbackFailure.Exception.Message)"
    }
}
