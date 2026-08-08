$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Assert-FileEquals {
    param(
        [Parameter(Mandatory)] [string] $Expected,
        [Parameter(Mandatory)] [string] $Actual
    )

    if ((Get-FileHash -LiteralPath $Expected).Hash -ne (Get-FileHash -LiteralPath $Actual).Hash) {
        throw "$Actual did not match"
    }
}

function New-Payload {
    param([Parameter(Mandatory)] [string] $Path)

    New-Item -ItemType Directory -Path $Path -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $Path 'velocity.exe'), 'velocity fixture')
    [IO.File]::WriteAllText((Join-Path $Path 'velocity-resolver.exe'), 'resolver fixture')
}

function New-Release {
    param(
        [Parameter(Mandatory)] [string] $ReleasePath,
        [Parameter(Mandatory)] [string] $PayloadPath,
        [Parameter(Mandatory)] [string] $AssetName
    )

    New-Item -ItemType Directory -Path $ReleasePath -Force | Out-Null
    $archive = Join-Path $ReleasePath $AssetName
    Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    $files = @(Get-ChildItem -LiteralPath $PayloadPath -File | Select-Object -ExpandProperty FullName)
    Compress-Archive -LiteralPath $files -DestinationPath $archive -CompressionLevel Optimal
    $digest = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $ReleasePath 'SHA256SUMS'), "$digest  $AssetName`n")
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$installer = Join-Path $repoRoot 'install.ps1'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("velocity-installer-{0}" -f [Guid]::NewGuid())
$target = 'x86_64-pc-windows-msvc'
$asset = "velocity-$target.zip"
$originalOS = $env:OS

try {
    $env:OS = 'Windows_NT'
    $payload = Join-Path $testRoot 'payload'
    $release = Join-Path $testRoot 'release'
    $installDir = Join-Path $testRoot 'install'

    # Given: a checksum manifest and ZIP containing both sibling executables.
    New-Payload -Path $payload
    New-Release -ReleasePath $release -PayloadPath $payload -AssetName $asset

    # When: a pinned release is installed from an offline release directory.
    & $installer -Version v1.2.3 -Target $target -InstallDir $installDir -ReleaseBaseUrl $release

    # Then: both verified executables are installed together.
    Assert-FileEquals -Expected (Join-Path $payload 'velocity.exe') -Actual (Join-Path $installDir 'velocity.exe')
    Assert-FileEquals -Expected (Join-Path $payload 'velocity-resolver.exe') `
        -Actual (Join-Path $installDir 'velocity-resolver.exe')

    # Given: a newer checksum-valid release for the same target.
    [IO.File]::WriteAllText((Join-Path $payload 'velocity.exe'), 'velocity upgraded')
    [IO.File]::WriteAllText((Join-Path $payload 'velocity-resolver.exe'), 'resolver upgraded')
    New-Release -ReleasePath $release -PayloadPath $payload -AssetName $asset

    # When: the installer is run again for that release.
    & $installer -Target $target -InstallDir $installDir -ReleaseBaseUrl $release

    # Then: both siblings are replaced by the verified release.
    Assert-FileEquals -Expected (Join-Path $payload 'velocity.exe') -Actual (Join-Path $installDir 'velocity.exe')
    Assert-FileEquals -Expected (Join-Path $payload 'velocity-resolver.exe') `
        -Actual (Join-Path $installDir 'velocity-resolver.exe')

    # Given: an explicit Windows target on a non-Windows host.
    $osInstall = Join-Path $testRoot 'os-install'
    $env:OS = 'NotWindows'

    # When: installation is attempted despite the host mismatch.
    $osFailed = $false
    try {
        & $installer -Target $target -InstallDir $osInstall -ReleaseBaseUrl $release
    }
    catch {
        $osFailed = $true
    }
    finally {
        $env:OS = 'Windows_NT'
    }

    # Then: no executable is published.
    if (-not $osFailed) { throw 'non-Windows host was accepted with an explicit target' }
    if (Test-Path -LiteralPath (Join-Path $osInstall 'velocity.exe')) {
        throw 'host mismatch published velocity.exe'
    }

    # Given: existing binaries and an archive whose bytes no longer match SHA256SUMS.
    $checksumInstall = Join-Path $testRoot 'checksum-install'
    New-Item -ItemType Directory -Path $checksumInstall -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $checksumInstall 'velocity.exe'), 'existing velocity')
    [IO.File]::WriteAllText((Join-Path $checksumInstall 'velocity-resolver.exe'), 'existing resolver')
    [IO.File]::AppendAllText((Join-Path $release $asset), 'tampered')

    # When: installation is attempted with the invalid checksum.
    $checksumFailed = $false
    try {
        & $installer -Target $target -InstallDir $checksumInstall -ReleaseBaseUrl $release
    }
    catch {
        $checksumFailed = $true
    }

    # Then: the mismatch is rejected and existing files remain untouched.
    if (-not $checksumFailed) { throw 'checksum mismatch was accepted' }
    if ([IO.File]::ReadAllText((Join-Path $checksumInstall 'velocity.exe')) -ne 'existing velocity') {
        throw 'checksum failure changed velocity.exe'
    }
    if ([IO.File]::ReadAllText((Join-Path $checksumInstall 'velocity-resolver.exe')) -ne 'existing resolver') {
        throw 'checksum failure changed velocity-resolver.exe'
    }

    # Given: an existing sibling pair and a valid replacement whose second publication will fail.
    $atomicPayload = Join-Path $testRoot 'atomic-payload'
    $atomicRelease = Join-Path $testRoot 'atomic-release'
    $atomicInstall = Join-Path $testRoot 'atomic-install'
    $publicationMarker = Join-Path $testRoot 'second-publication'
    New-Payload -Path $atomicPayload
    New-Release -ReleasePath $atomicRelease -PayloadPath $atomicPayload -AssetName $asset
    New-Item -ItemType Directory -Path $atomicInstall -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $atomicInstall 'velocity.exe'), 'old velocity')
    [IO.File]::WriteAllText((Join-Path $atomicInstall 'velocity-resolver.exe'), 'old resolver')
    $global:VelocityFailResolverDestination = Join-Path $atomicInstall 'velocity-resolver.exe'
    $global:VelocityPublicationMarker = $publicationMarker
    function global:Move-Item {
        [CmdletBinding()]
        param(
            [Parameter(Mandatory)] [string] $LiteralPath,
            [Parameter(Mandatory)] [string] $Destination,
            [switch] $Force
        )

        if ($Destination -ceq $global:VelocityFailResolverDestination -and
            -not (Test-Path -LiteralPath $global:VelocityPublicationMarker)) {
            [IO.File]::WriteAllText($global:VelocityPublicationMarker, $Destination)
            throw 'injected second publication failure'
        }
        Microsoft.PowerShell.Management\Move-Item @PSBoundParameters
    }

    # When: only the final velocity-resolver publication is rejected.
    $publicationFailed = $false
    try {
        & $installer -Target $target -InstallDir $atomicInstall -ReleaseBaseUrl $atomicRelease
    }
    catch {
        $publicationFailed = $true
    }
    finally {
        Remove-Item Function:\Move-Item -Force
        Remove-Variable VelocityFailResolverDestination -Scope Global
        Remove-Variable VelocityPublicationMarker -Scope Global
    }

    # Then: the injected seam was reached and the complete prior pair was restored.
    if (-not $publicationFailed) { throw 'second publication failure was accepted' }
    if ([IO.File]::ReadAllText($publicationMarker) -cne (Join-Path $atomicInstall 'velocity-resolver.exe')) {
        throw 'publication failure occurred outside the intended resolver seam'
    }
    if ([IO.File]::ReadAllText((Join-Path $atomicInstall 'velocity.exe')) -cne 'old velocity') {
        throw 'second publication failure did not restore velocity.exe'
    }
    if ([IO.File]::ReadAllText((Join-Path $atomicInstall 'velocity-resolver.exe')) -cne 'old resolver') {
        throw 'second publication failure changed velocity-resolver.exe'
    }

    # Given: another publisher owns the per-installation lock.
    $lockedInstall = Join-Path $testRoot 'locked-install'
    New-Item -ItemType Directory -Path $lockedInstall -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $lockedInstall 'velocity.exe'), 'locked velocity')
    [IO.File]::WriteAllText((Join-Path $lockedInstall 'velocity-resolver.exe'), 'locked resolver')
    $heldLock = [IO.File]::Open(
        (Join-Path $lockedInstall '.velocity-install.lock'),
        [IO.FileMode]::OpenOrCreate,
        [IO.FileAccess]::ReadWrite,
        [IO.FileShare]::None
    )

    # When: a concurrent installation attempts to publish into the same directory.
    $lockFailed = $false
    try {
        try {
            & $installer -Target $target -InstallDir $lockedInstall -ReleaseBaseUrl $atomicRelease
        }
        catch {
            $lockFailed = $true
        }
    }
    finally {
        $heldLock.Dispose()
    }

    # Then: it is rejected before either existing sibling changes.
    if (-not $lockFailed) { throw 'concurrent publisher bypassed the publication lock' }
    if ([IO.File]::ReadAllText((Join-Path $lockedInstall 'velocity.exe')) -cne 'locked velocity') {
        throw 'publication lock failure changed velocity.exe'
    }
    if ([IO.File]::ReadAllText((Join-Path $lockedInstall 'velocity-resolver.exe')) -cne 'locked resolver') {
        throw 'publication lock failure changed velocity-resolver.exe'
    }

    # Given: an HTTPS release source whose local endpoint refuses connections.
    $onlineError = $null
    try {
        & $installer -Target $target -InstallDir (Join-Path $testRoot 'online-install') `
            -ReleaseBaseUrl 'https://127.0.0.1:1'
    }
    catch {
        $onlineError = $_.Exception.Message
    }

    # Then: the source is classified as HTTPS rather than as a nonexistent PowerShell drive.
    if ($null -eq $onlineError) { throw 'unreachable HTTPS fixture unexpectedly succeeded' }
    if ($onlineError -match "drive.*https") { throw 'HTTPS release source was parsed as a provider path' }

    # Given: a checksum-valid ZIP with an unexpected third entry.
    $layoutPayload = Join-Path $testRoot 'layout-payload'
    $layoutRelease = Join-Path $testRoot 'layout-release'
    $layoutInstall = Join-Path $testRoot 'layout-install'
    New-Payload -Path $layoutPayload
    [IO.File]::WriteAllText((Join-Path $layoutPayload 'extra'), 'unexpected')
    New-Release -ReleasePath $layoutRelease -PayloadPath $layoutPayload -AssetName $asset

    # When: the unexpected layout is installed.
    $layoutFailed = $false
    try {
        & $installer -Target $target -InstallDir $layoutInstall -ReleaseBaseUrl $layoutRelease
    }
    catch {
        $layoutFailed = $true
    }

    # Then: no executable is published.
    if (-not $layoutFailed) { throw 'unexpected ZIP entry was accepted' }
    if (Test-Path -LiteralPath (Join-Path $layoutInstall 'velocity.exe')) {
        throw 'invalid ZIP published velocity.exe'
    }

    Write-Output 'install.ps1 tests passed'
}
finally {
    if ($null -eq $originalOS) {
        Remove-Item Env:OS -ErrorAction SilentlyContinue
    }
    else {
        $env:OS = $originalOS
    }
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
