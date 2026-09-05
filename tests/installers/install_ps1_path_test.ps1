$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$installer = Join-Path (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path 'install.ps1'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("velocity-path-{0}" -f [Guid]::NewGuid())
$savedEnvironment = @{}
foreach ($name in @('PATH', 'VELOCITY_NO_MODIFY_PATH', 'VELOCITY_INSTALL_DIR', 'VELOCITY_RELEASE_BASE_URL', 'VELOCITY_TARGET')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
$hadUserPath = $key.GetValueNames() -contains 'Path'
$savedUserPath = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
$savedKind = if ($hadUserPath) { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
try {
    New-Item -ItemType Directory -Path "$testRoot/payload", "$testRoot/release" -Force | Out-Null
    [IO.File]::WriteAllText("$testRoot/payload/velocity.exe", 'fixture')
    [IO.File]::WriteAllText("$testRoot/payload/velocity-resolver.exe", 'resolver fixture')
    $asset = 'velocity-x86_64-pc-windows-msvc.zip'
    Compress-Archive -LiteralPath "$testRoot/payload/velocity.exe", "$testRoot/payload/velocity-resolver.exe" -DestinationPath "$testRoot/release/$asset"
    $digest = (Get-FileHash -LiteralPath "$testRoot/release/$asset" -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$testRoot/release/SHA256SUMS", "$digest  $asset`n")
    $directory = Join-Path $testRoot 'bin with spaces'
    $env:VELOCITY_INSTALL_DIR = $directory
    $env:VELOCITY_RELEASE_BASE_URL = "$testRoot/release"
    $env:VELOCITY_TARGET = 'x86_64-pc-windows-msvc'
    $env:VELOCITY_NO_MODIFY_PATH = '0'
    $key.SetValue('Path', '%SystemRoot%\TestExisting', [Microsoft.Win32.RegistryValueKind]::ExpandString)
    $beforeProcess = $env:PATH
    function Invoke-RestMethod {
        param([string] $Uri)
        if ($Uri -cne 'https://raw.githubusercontent.com/Hunter2030ZeRo/Velocity/main/install.ps1') { throw 'Unexpected URL' }
        Get-Content -LiteralPath $installer -Raw
    }
    # The documented pipeline must update this process as well as persistent PATH.
    foreach ($attempt in 1..2) {
        & { irm https://raw.githubusercontent.com/Hunter2030ZeRo/Velocity/main/install.ps1 | iex }
    }
    $actualUser = [string] $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($actualUser -cne "%SystemRoot%\TestExisting;$directory") { throw 'User PATH was duplicated, expanded or overwritten' }
    if ($key.GetValueKind('Path') -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) { throw 'Expandable PATH registry type was lost' }
    if ($env:PATH -cne "$($beforeProcess.TrimEnd(';'));$directory") { throw 'Process PATH was not updated exactly once' }
    if ((Get-Command velocity.exe -CommandType Application).Source -ne "$directory\velocity.exe") { throw 'velocity is not available in this session' }

    # Comparison respects Windows case, trailing separators and variable entries.
    . $installer
    if (-not (Test-PathEntry -Value ($directory.ToUpperInvariant() + '\') -Directory $directory)) { throw 'Case or trailing separator was not normalized' }
    if (-not (Test-PathEntry -Value '%VELOCITY_INSTALL_DIR%' -Directory $directory)) { throw 'Variable PATH entry was not recognized' }
    if (Test-PathEntry -Value ($directory + '-other') -Directory $directory) { throw 'PATH matched only a substring' }

    # Opt-out still installs files without changing either PATH scope.
    $optOut = Join-Path $testRoot 'opt-out'
    & $installer -Target $env:VELOCITY_TARGET -InstallDir $optOut -ReleaseBaseUrl "$testRoot/release" -NoModifyPath
    if (-not (Test-Path -LiteralPath "$optOut/velocity.exe")) { throw 'Opt-out prevented installation' }
    if ([string] $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -cne $actualUser) { throw 'Opt-out changed user PATH' }
    if ($env:PATH -cne "$($beforeProcess.TrimEnd(';'));$directory") { throw 'Opt-out changed process PATH' }
    Write-Output 'install.ps1 PATH tests passed'
}
finally {
    try {
        if ($hadUserPath) { $key.SetValue('Path', $savedUserPath, $savedKind) }
        else { $key.DeleteValue('Path', $false) }
    }
    finally {
        $key.Dispose()
        foreach ($name in $savedEnvironment.Keys) {
            [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
        }
        Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
