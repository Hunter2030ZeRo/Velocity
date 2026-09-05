# allow: SIZE_OK - standalone distribution artifact with security helpers embedded.
param(
    [string] $Version,
    [string] $Target,
    [string] $InstallDir,
    [string] $ReleaseBaseUrl,
    [string] $Repository,
    [switch] $NoModifyPath,
    [switch] $PathOnly
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.Net.Http
$script:VelocityInstallerHttpHandler = $null

function Resolve-HttpsRedirect {
    param(
        [Parameter(Mandatory)] [Uri] $Current,
        [Uri] $Location
    )

    if ($null -eq $Location) { throw 'HTTPS redirect response omitted Location' }
    $next = [Uri]::new($Current, $Location)
    if (-not $next.IsAbsoluteUri -or $next.Scheme -cne 'https') {
        throw "HTTPS redirect attempted an insecure destination: $next"
    }
    return $next
}

function Save-HttpsFile {
    param(
        [Parameter(Mandatory)] [Uri] $Uri,
        [Parameter(Mandatory)] [string] $Destination,
        [Parameter(Mandatory)] [long] $MaximumBytes,
        [ValidateScript({ $_ -gt [TimeSpan]::Zero })]
        [TimeSpan] $Timeout = ([TimeSpan]::FromMinutes(5))
    )

    $handler = $script:VelocityInstallerHttpHandler
    $ownsHandler = $null -eq $handler
    if ($ownsHandler) {
        $handler = [Net.Http.HttpClientHandler]::new()
    }
    if ($handler -is [Net.Http.HttpClientHandler]) { $handler.AllowAutoRedirect = $false }
    $client = [Net.Http.HttpClient]::new($handler, $false)
    $deadline = [Threading.CancellationTokenSource]::new()
    $deadline.CancelAfter($Timeout)
    $deadlineTask = [Threading.Tasks.Task]::Delay(
        [Threading.Timeout]::Infinite,
        $deadline.Token
    )
    try {
        $current = $Uri
        $redirects = 0
        while ($true) {
            if (-not $current.IsAbsoluteUri -or $current.Scheme -cne 'https') {
                throw "Release URL must use HTTPS: $current"
            }
            $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Get, $current)
            $response = $null
            try {
                $response = $client.SendAsync(
                    $request,
                    [Net.Http.HttpCompletionOption]::ResponseHeadersRead,
                    $deadline.Token
                ).GetAwaiter().GetResult()
                if ([int] $response.StatusCode -in @(301, 302, 303, 307, 308)) {
                    if ($redirects -ge 10) { throw 'Release download exceeded 10 redirects' }
                    $current = Resolve-HttpsRedirect -Current $current -Location $response.Headers.Location
                    $redirects++
                    continue
                }
                [void] $response.EnsureSuccessStatusCode()
                if ($response.Content.Headers.ContentLength -gt $MaximumBytes) {
                    throw "Release file exceeds $MaximumBytes bytes"
                }
                $bodyStream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
                $output = [IO.File]::Create($Destination)
                $complete = $false
                try {
                    $buffer = [byte[]]::new(65536)
                    $total = [long] 0
                    while ($true) {
                        $readTask = $bodyStream.ReadAsync($buffer, 0, $buffer.Length, $deadline.Token)
                        $winner = [Threading.Tasks.Task]::WhenAny(
                            [Threading.Tasks.Task[]] @($readTask, $deadlineTask)
                        ).GetAwaiter().GetResult()
                        if ([object]::ReferenceEquals($winner, $deadlineTask) -or
                            $deadline.IsCancellationRequested) {
                            $bodyStream.Dispose()
                            $response.Dispose()
                            throw [TimeoutException]::new('Release download exceeded its absolute deadline')
                        }
                        $read = $readTask.GetAwaiter().GetResult()
                        if ($read -le 0) { break }
                        $total += $read
                        if ($total -gt $MaximumBytes) { throw "Release file exceeds $MaximumBytes bytes" }
                        $output.Write($buffer, 0, $read)
                    }
                    $complete = $true
                }
                finally {
                    $output.Dispose()
                    $bodyStream.Dispose()
                    if (-not $complete -and [IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
                }
                return
            }
            finally {
                if ($null -ne $response) { $response.Dispose() }
                $request.Dispose()
            }
        }
    }
    finally {
        $deadline.Cancel()
        $deadline.Dispose()
        $client.Dispose()
        if ($ownsHandler) { $handler.Dispose() }
    }
}

function Copy-ReleaseFile {
    param(
        [Parameter(Mandatory)] [string] $Base,
        [Parameter(Mandatory)] [string] $Name,
        [Parameter(Mandatory)] [string] $Destination
    )

    $maximumBytes = if ($Name -ceq 'SHA256SUMS') { 1MB } else { 256MB }
    if ($Base.StartsWith('\\') -or $Base.StartsWith('//')) {
        throw 'Local release directory must not be a UNC or device path'
    }
    if ([IO.Path]::IsPathRooted($Base) -and [IO.Directory]::Exists($Base)) {
        $baseItem = Get-Item -LiteralPath $Base -Force
        if ($baseItem.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw 'Local release directory must not be a reparse point'
        }
        $source = Join-Path $Base $Name
        $sourceItem = Get-Item -LiteralPath $source -Force
        if ($sourceItem.PSIsContainer -or ($sourceItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
            $sourceItem.Length -gt $maximumBytes) {
            throw "Release file is invalid or exceeds $maximumBytes bytes: $source"
        }
        $sourceStream = [IO.File]::Open($source, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
        $output = [IO.File]::Create($Destination)
        $complete = $false
        try {
            $buffer = [byte[]]::new(65536)
            $total = [long] 0
            while (($read = $sourceStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                $total += $read
                if ($total -gt $maximumBytes) { throw "Release file exceeds $maximumBytes bytes: $source" }
                $output.Write($buffer, 0, $read)
            }
            $complete = $true
        }
        finally {
            $output.Dispose()
            $sourceStream.Dispose()
            if (-not $complete -and [IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
        }
        return
    }

    $baseUri = [Uri] $Base
    if (-not $baseUri.IsAbsoluteUri -or $baseUri.Scheme -cne 'https' -or
        $baseUri.UserInfo -or $baseUri.Query -or $baseUri.Fragment) {
        throw 'Release source must be an HTTPS directory URL or an explicit local directory'
    }
    $source = [Uri]::new($baseUri.AbsoluteUri.TrimEnd('/') + '/' + [Uri]::EscapeDataString($Name))
    Save-HttpsFile -Uri $source -Destination $Destination -MaximumBytes $maximumBytes
}

function Get-ExpectedHash {
    param(
        [Parameter(Mandatory)] [string] $Manifest,
        [Parameter(Mandatory)] [string] $AssetName
    )

    $assetPattern = [Regex]::Escape($AssetName)
    $pattern = "^(?<Hash>[0-9A-Fa-f]{64})[ `t]+\*?$assetPattern$"
    $expected = $null
    foreach ($line in [IO.File]::ReadLines($Manifest)) {
        $match = [Regex]::Match($line, $pattern)
        if (-not $match.Success) { continue }
        if ($null -ne $expected) { throw "SHA256SUMS contains duplicate entries for $AssetName" }
        $expected = $match.Groups['Hash'].Value.ToLowerInvariant()
    }
    if ($null -eq $expected) { throw "SHA256SUMS does not contain a checksum for $AssetName" }
    return $expected
}

function Assert-SafeZipDirectory {
    param([Parameter(Mandatory)] [string] $Archive)

    $stream = [IO.File]::Open($Archive, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        if ($stream.Length -lt 22) { throw 'Release ZIP is truncated' }
        [void] $stream.Seek(-22, [IO.SeekOrigin]::End)
        $reader = [IO.BinaryReader]::new($stream, [Text.Encoding]::UTF8, $true)
        try {
            $signature = $reader.ReadUInt32()
            $disk = $reader.ReadUInt16()
            $directoryDisk = $reader.ReadUInt16()
            $diskEntries = $reader.ReadUInt16()
            $totalEntries = $reader.ReadUInt16()
            $directorySize = $reader.ReadUInt32()
            $directoryOffset = $reader.ReadUInt32()
            $commentLength = $reader.ReadUInt16()
        }
        finally {
            $reader.Dispose()
        }
        if ($signature -ne 0x06054b50 -or $disk -ne 0 -or $directoryDisk -ne 0 -or
            $diskEntries -ne 2 -or $totalEntries -ne 2 -or $commentLength -ne 0) {
            throw 'Release ZIP must be single-disk, non-ZIP64, comment-free, and contain two entries'
        }
        if ($directorySize -gt 1MB -or ([long] $directoryOffset + $directorySize) -ne ($stream.Length - 22)) {
            throw 'Release ZIP central directory is invalid or exceeds 1 MiB'
        }
    }
    finally {
        $stream.Dispose()
    }
}

function Expand-VelocityArchive {
    param(
        [Parameter(Mandatory)] [string] $Archive,
        [Parameter(Mandatory)] [string] $Destination
    )

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    Assert-SafeZipDirectory -Archive $Archive
    $expectedNames = @('velocity.exe', 'velocity-resolver.exe')
    $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
    try {
        $entries = @($zip.Entries)
        if ($entries.Count -ne $expectedNames.Count) { throw 'Release ZIP must contain exactly two executables' }
        $declaredExpandedBytes = [long] 0
        $actualExpandedBytes = [long] 0
        foreach ($expectedName in $expectedNames) {
            $matches = @($entries | Where-Object { $_.FullName -ceq $expectedName })
            if ($matches.Count -ne 1 -or $matches[0].Length -le 0) {
                throw "Release ZIP must contain $expectedName exactly once as a non-empty file"
            }
            if ($matches[0].Length -gt 128MB) { throw "Release ZIP entry exceeds 128 MiB: $expectedName" }
            $declaredExpandedBytes += $matches[0].Length
            if ($declaredExpandedBytes -gt 256MB) { throw 'Release ZIP expands beyond 256 MiB' }
            $unixType = (($matches[0].ExternalAttributes -shr 16) -band 0xF000)
            if ($unixType -eq 0xA000) { throw "Release ZIP entry must not be a symbolic link: $expectedName" }
            $entryStream = $matches[0].Open()
            $destinationPath = Join-Path $Destination $expectedName
            $output = [IO.File]::Open($destinationPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
            $complete = $false
            try {
                $buffer = [byte[]]::new(65536)
                $entryBytes = [long] 0
                while (($read = $entryStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                    $entryBytes += $read
                    if ($entryBytes -gt 128MB -or ($actualExpandedBytes + $entryBytes) -gt 256MB) {
                        throw "Release ZIP expansion limit exceeded: $expectedName"
                    }
                    $output.Write($buffer, 0, $read)
                }
                $actualExpandedBytes += $entryBytes
                $complete = $true
            }
            finally {
                $output.Dispose()
                $entryStream.Dispose()
                if (-not $complete -and [IO.File]::Exists($destinationPath)) { [IO.File]::Delete($destinationPath) }
            }
        }
    }
    finally {
        $zip.Dispose()
    }
}

function Restore-PublishedPair {
    param(
        [Parameter(Mandatory)] [hashtable] $PreviousFiles,
        [Parameter(Mandatory)] [string] $InstallDir
    )

    foreach ($name in @('velocity.exe', 'velocity-resolver.exe')) {
        $destination = Join-Path $InstallDir $name
        if ($PreviousFiles.ContainsKey($name)) {
            Copy-Item -LiteralPath $PreviousFiles[$name] -Destination $destination -Force
        }
        elseif (Test-Path -LiteralPath $destination) {
            Remove-Item -LiteralPath $destination -Force
        }
    }
}

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

function Test-PathEntry {
    param([AllowNull()] [string] $Value, [string] $Directory)
    foreach ($entry in @($Value -split ';')) {
        $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"')).TrimEnd('\', '/')
        if ([string]::Equals($expanded, $Directory.TrimEnd('\', '/'), [StringComparison]::OrdinalIgnoreCase)) {
            return $true
        }
    }
    return $false
}

function Add-VelocityToPath {
    param([Parameter(Mandatory)] [string] $Directory)
    if ($Directory.IndexOfAny([char[]] ";`r`n") -ge 0) {
        throw 'The installation directory cannot be represented as a PATH entry'
    }
    # Read without expanding existing %VARIABLE% entries, and never copy the
    # combined process/system PATH into the per-user registry value.
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    $userPath = ''
    try {
        $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
        if ($key.GetValueNames() -contains 'Path') {
            $kind = $key.GetValueKind('Path')
            if ($kind -notin @([Microsoft.Win32.RegistryValueKind]::String, [Microsoft.Win32.RegistryValueKind]::ExpandString)) {
                throw 'Existing user PATH is not a string registry value'
            }
        }
        $userPath = [string] $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if (-not (Test-PathEntry -Value $userPath -Directory $Directory)) {
            $newPath = if ([string]::IsNullOrEmpty($userPath)) { $Directory } else { "$($userPath.TrimEnd(';'));$Directory" }
            $key.SetValue('Path', $newPath, $kind)
        }
    }
    finally { $key.Dispose() }
    if (-not (Test-PathEntry -Value $env:PATH -Directory $Directory)) {
        $env:PATH = if ([string]::IsNullOrEmpty($env:PATH)) { $Directory } else { "$($env:PATH.TrimEnd(';'));$Directory" }
    }
    # Let Explorer and other launchers refresh their environment for new shells.
    try {
        if (-not ('VelocityInstaller.EnvironmentNotification' -as [type])) {
            Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace VelocityInstaller {
    public static class EnvironmentNotification {
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        public static extern IntPtr SendMessageTimeoutW(IntPtr window, uint message,
            UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result);
    }
}
'@
        }
        $result = [UIntPtr]::Zero
        [void] [VelocityInstaller.EnvironmentNotification]::SendMessageTimeoutW(
            [IntPtr] 0xffff, 0x1a, [UIntPtr]::Zero, 'Environment', 2, 1000, [ref] $result)
    }
    catch { Write-Warning 'PATH is saved. Restart your terminal application if new windows do not see it.' }
    Write-Output 'Configured user PATH and the current PowerShell session.'
}

# Loading a file with dot-sourcing exposes helpers for tests. Invoke-Expression
# also runs in the caller's scope, but must execute the installation itself.
if ($MyInvocation.InvocationName -eq '.' -and
    $MyInvocation.MyCommand -is [Management.Automation.ExternalScriptInfo]) { return }

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
if ($PathOnly) {
    if ($NoModifyPath) { throw '-PathOnly cannot be combined with -NoModifyPath' }
    $installItem = Get-Item -LiteralPath $InstallDir -Force
    if (-not $installItem.PSIsContainer -or ($installItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "Installation directory must be a non-reparse directory: $InstallDir"
    }
    # Explicit PATH repair overrides the automatic-installation opt-out.
    # Invoke this scriptblock in the current PowerShell to update that session.
    Add-VelocityToPath -Directory $InstallDir
    Write-Output "Package command directory: $InstallDir"
    return
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
    if (-not $NoModifyPath -and $env:VELOCITY_NO_MODIFY_PATH -ne '1') {
        try { Add-VelocityToPath -Directory $InstallDir }
        catch { Write-Warning "Velocity is installed, but automatic PATH configuration failed: $($_.Exception.Message). Add $InstallDir to PATH manually." }
    }
    elseif (-not (Test-PathEntry -Value $env:PATH -Directory $InstallDir)) {
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
