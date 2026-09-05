$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$installer = Join-Path $repoRoot 'install.ps1'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("velocity-recovery-{0}" -f [Guid]::NewGuid())
$target = 'x86_64-pc-windows-msvc'
$asset = "velocity-$target.zip"
$originalOS = $env:OS
$originalNoModifyPath = $env:VELOCITY_NO_MODIFY_PATH
$env:VELOCITY_NO_MODIFY_PATH = '1'

try {
    $env:OS = 'Windows_NT'
    $payload = Join-Path $testRoot 'payload'
    $release = Join-Path $testRoot 'release'
    $installDir = Join-Path $testRoot 'install'
    New-Item -ItemType Directory -Path $payload, $release, $installDir | Out-Null
    [IO.File]::WriteAllText((Join-Path $payload 'velocity.exe'), 'new velocity')
    [IO.File]::WriteAllText((Join-Path $payload 'velocity-resolver.exe'), 'new resolver')
    $archive = Join-Path $release $asset
    Compress-Archive -LiteralPath @(
        (Join-Path $payload 'velocity.exe'),
        (Join-Path $payload 'velocity-resolver.exe')
    ) -DestinationPath $archive
    $digest = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $release 'SHA256SUMS'), "$digest  $asset`n")
    [IO.File]::WriteAllText((Join-Path $installDir 'velocity.exe'), 'old velocity')
    [IO.File]::WriteAllText((Join-Path $installDir 'velocity-resolver.exe'), 'old resolver')

    $global:VelocityRecoveryResolver = Join-Path $installDir 'velocity-resolver.exe'
    $global:VelocityRecoveryCLI = Join-Path $installDir 'velocity.exe'
    function global:Move-Item {
        [CmdletBinding()]
        param(
            [Parameter(Mandatory)] [string] $LiteralPath,
            [Parameter(Mandatory)] [string] $Destination,
            [switch] $Force
        )

        if ($Destination -ceq $global:VelocityRecoveryResolver) {
            throw 'injected resolver publication failure'
        }
        Microsoft.PowerShell.Management\Move-Item @PSBoundParameters
    }
    function global:Copy-Item {
        [CmdletBinding()]
        param(
            [Parameter(Mandatory)] [string] $LiteralPath,
            [Parameter(Mandatory)] [string] $Destination,
            [switch] $Force
        )

        if ($Destination -ceq $global:VelocityRecoveryCLI -and
            [IO.Path]::GetFileName($LiteralPath) -ceq '.previous-velocity.exe') {
            throw 'injected rollback failure'
        }
        Microsoft.PowerShell.Management\Copy-Item @PSBoundParameters
    }

    $failed = $false
    try {
        & $installer -Target $target -InstallDir $installDir -ReleaseBaseUrl $release
    }
    catch {
        $failed = $true
    }
    finally {
        Remove-Item Function:\Move-Item -Force
        Remove-Item Function:\Copy-Item -Force
        Remove-Variable VelocityRecoveryResolver -Scope Global
        Remove-Variable VelocityRecoveryCLI -Scope Global
    }

    if (-not $failed) { throw 'rollback-failure injection unexpectedly succeeded' }
    $marker = Join-Path $installDir '.velocity-recovery-required'
    if (-not (Test-Path -LiteralPath $marker -PathType Leaf)) { throw 'recovery marker was removed' }
    $stage = [IO.File]::ReadAllText($marker)
    if (-not [IO.Directory]::Exists($stage)) { throw 'recovery stage was removed' }
    if (-not [IO.File]::Exists((Join-Path $stage '.previous-velocity.exe'))) {
        throw 'prior velocity.exe backup was removed'
    }
    if (-not [IO.File]::Exists((Join-Path $stage '.previous-velocity-resolver.exe'))) {
        throw 'prior velocity-resolver.exe backup was removed'
    }

    Write-Output 'install.ps1 recovery tests passed'
}
finally {
    $env:VELOCITY_NO_MODIFY_PATH = $originalNoModifyPath
    if ($null -eq $originalOS) { Remove-Item Env:OS -ErrorAction SilentlyContinue }
    else { $env:OS = $originalOS }
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
