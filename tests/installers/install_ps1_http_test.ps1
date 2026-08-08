$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ("velocity-http-{0}" -f [Guid]::NewGuid())
New-Item -ItemType Directory -Path $testRoot | Out-Null
try {
    $installer = Join-Path $testRoot 'install.ps1'
    Copy-Item -LiteralPath (Join-Path $repoRoot 'install.ps1') -Destination $installer
    try { . $installer } catch { }
    if ($null -eq (Get-Command Save-HttpsFile -ErrorAction SilentlyContinue)) {
        throw 'manual HTTPS downloader is not available'
    }

    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

public sealed class InstallerRedirectHandler : HttpMessageHandler
{
    private readonly bool downgrade;
    private readonly List<Uri> requests = new List<Uri>();
    public List<Uri> Requests { get { return requests; } }
    public bool Oversized { get; set; }

    public InstallerRedirectHandler(bool downgrade)
    {
        this.downgrade = downgrade;
    }

    protected override Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        Requests.Add(request.RequestUri);
        var response = new HttpResponseMessage();
        if (Oversized)
        {
            response.StatusCode = HttpStatusCode.OK;
            response.Content = new ByteArrayContent(new byte[2 * 1024 * 1024]);
        }
        else if (Requests.Count == 1)
        {
            response.StatusCode = HttpStatusCode.Found;
            response.Headers.Location = downgrade
                ? new Uri("http://fixture.invalid/final")
                : new Uri("/middle", UriKind.Relative);
        }
        else if (Requests.Count == 2)
        {
            response.StatusCode = HttpStatusCode.TemporaryRedirect;
            response.Headers.Location = new Uri("https://fixture.invalid/final");
        }
        else
        {
            response.StatusCode = HttpStatusCode.OK;
            response.Content = new ByteArrayContent(Encoding.UTF8.GetBytes("verified fixture"));
        }
        return Task.FromResult(response);
    }
}

public sealed class InstallerDelayedStream : System.IO.Stream
{
    public bool IsDisposed { get; private set; }
    public override bool CanRead { get { return true; } }
    public override bool CanSeek { get { return false; } }
    public override bool CanWrite { get { return false; } }
    public override long Length { get { throw new NotSupportedException(); } }
    public override long Position
    {
        get { throw new NotSupportedException(); }
        set { throw new NotSupportedException(); }
    }

    public override void Flush() { }
    public override int Read(byte[] buffer, int offset, int count)
    {
        throw new NotSupportedException();
    }
    public override long Seek(long offset, System.IO.SeekOrigin origin)
    {
        throw new NotSupportedException();
    }
    public override void SetLength(long value) { throw new NotSupportedException(); }
    public override void Write(byte[] buffer, int offset, int count)
    {
        throw new NotSupportedException();
    }
    public override async Task<int> ReadAsync(
        byte[] buffer,
        int offset,
        int count,
        CancellationToken cancellationToken)
    {
        await Task.Delay(750).ConfigureAwait(false);
        return 0;
    }
    protected override void Dispose(bool disposing)
    {
        IsDisposed = true;
        base.Dispose(disposing);
    }
}

public sealed class InstallerDeadlineHandler : HttpMessageHandler
{
    private readonly InstallerDelayedStream body = new InstallerDelayedStream();
    public InstallerDelayedStream Body { get { return body; } }

    protected override Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        var response = new HttpResponseMessage(HttpStatusCode.OK);
        response.Content = new StreamContent(Body);
        return Task.FromResult(response);
    }
}
'@

    # An HTTPS-to-HTTP redirect must fail before dispatching the second request.
    $downgradeHandler = [InstallerRedirectHandler]::new($true)
    $script:VelocityInstallerHttpHandler = $downgradeHandler
    $downgradeFailed = $false
    try {
        Save-HttpsFile -Uri ([Uri] 'https://fixture.invalid/start') `
            -Destination (Join-Path $testRoot 'downgrade') -MaximumBytes 1MB
    }
    catch {
        $downgradeFailed = $true
    }
    if (-not $downgradeFailed -or $downgradeHandler.Requests.Count -ne 1) {
        throw 'HTTPS downgrade was dispatched or accepted'
    }

    # An oversized HTTP response is rejected before a destination is committed.
    $oversizedHandler = [InstallerRedirectHandler]::new($false)
    $oversizedHandler.Oversized = $true
    $script:VelocityInstallerHttpHandler = $oversizedHandler
    $oversizedDestination = Join-Path $testRoot 'oversized'
    $oversizedFailed = $false
    try {
        Save-HttpsFile -Uri ([Uri] 'https://fixture.invalid/oversized') `
            -Destination $oversizedDestination -MaximumBytes 1MB
    }
    catch {
        $oversizedFailed = $true
    }
    if (-not $oversizedFailed -or [IO.File]::Exists($oversizedDestination)) {
        throw 'oversized HTTPS response was accepted or left a partial file'
    }

    # Relative and absolute HTTPS hops are followed, bounded, and saved exactly once.
    $redirectHandler = [InstallerRedirectHandler]::new($false)
    $script:VelocityInstallerHttpHandler = $redirectHandler
    $destination = Join-Path $testRoot 'download'
    Save-HttpsFile -Uri ([Uri] 'https://fixture.invalid/start') `
        -Destination $destination -MaximumBytes 1MB
    if ($redirectHandler.Requests.Count -ne 3) { throw 'HTTPS redirect chain was not followed' }
    if (@($redirectHandler.Requests | Where-Object { $_.Scheme -cne 'https' }).Count -ne 0) {
        throw 'redirect chain dispatched a non-HTTPS request'
    }
    if ([IO.File]::ReadAllText($destination) -cne 'verified fixture') {
        throw 'redirected HTTPS response body was not saved'
    }

    # A body read that ignores cancellation is aborted by the absolute deadline.
    $deadlineHandler = [InstallerDeadlineHandler]::new()
    $script:VelocityInstallerHttpHandler = $deadlineHandler
    $deadlineDestination = Join-Path $testRoot 'deadline'
    $deadlineError = $null
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    try {
        Save-HttpsFile -Uri ([Uri] 'https://fixture.invalid/stalled') `
            -Destination $deadlineDestination -MaximumBytes 1MB `
            -Timeout ([TimeSpan]::FromMilliseconds(50))
    }
    catch {
        $deadlineError = $_.Exception
    }
    finally {
        $stopwatch.Stop()
    }
    $isTimeout = $deadlineError -is [TimeoutException] -or
        ($null -ne $deadlineError -and $deadlineError.InnerException -is [TimeoutException])
    if (-not $isTimeout) { throw 'stalled HTTPS body did not raise a timeout' }
    if ($stopwatch.Elapsed -ge [TimeSpan]::FromMilliseconds(500)) {
        throw 'stalled HTTPS body exceeded its absolute deadline'
    }
    if (-not $deadlineHandler.Body.IsDisposed) {
        throw 'stalled HTTPS body stream was not aborted'
    }
    if ([IO.File]::Exists($deadlineDestination)) {
        throw 'stalled HTTPS body left a partial file'
    }

    # A local release file is bounded while being copied, not only before opening.
    $localRelease = Join-Path $testRoot 'local-release'
    New-Item -ItemType Directory -Path $localRelease | Out-Null
    $largeManifest = Join-Path $localRelease 'SHA256SUMS'
    $largeStream = [IO.File]::Create($largeManifest)
    try { $largeStream.SetLength(1MB + 1) } finally { $largeStream.Dispose() }
    $localFailed = $false
    try {
        Copy-ReleaseFile -Base $localRelease -Name 'SHA256SUMS' `
            -Destination (Join-Path $testRoot 'local-copy')
    }
    catch {
        $localFailed = $true
    }
    if (-not $localFailed) { throw 'oversized local manifest was accepted' }

    Write-Output 'install.ps1 HTTP tests passed'
}
finally {
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}
