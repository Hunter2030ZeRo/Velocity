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
