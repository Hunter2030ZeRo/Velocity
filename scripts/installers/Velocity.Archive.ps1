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
