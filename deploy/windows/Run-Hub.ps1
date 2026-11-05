param(
    [string]$InstallDirectory = 'C:\Tools\cliproxyapi'
)

$ErrorActionPreference = 'Stop'
$mutex = New-Object System.Threading.Mutex($false, 'Global\CLIProxyAPI-PersonalHub')
$acquired = $false
try { $acquired = $mutex.WaitOne(0) }
catch [System.Threading.AbandonedMutexException] { $acquired = $true }
if (-not $acquired) { $mutex.Dispose(); exit 0 }
try {
    $binary = Join-Path $InstallDirectory 'cli-proxy-api-hub.exe'
    $config = Join-Path $InstallDirectory 'hub.yaml'
    if (-not (Test-Path -LiteralPath $binary) -or -not (Test-Path -LiteralPath $config)) {
        throw 'Hub executable or configuration is missing.'
    }
    Set-Location -LiteralPath $InstallDirectory
    while ($true) {
        $log = Join-Path $InstallDirectory ('hub-' + (Get-Date -Format 'yyyy-MM-dd') + '.log')
        try {
            & $binary -config $config 2>&1 | Out-File -LiteralPath $log -Append -Encoding utf8
        } catch {
            'Hub process failed; retrying in five seconds.' | Out-File -LiteralPath $log -Append -Encoding utf8
        }
        Start-Sleep -Seconds 5
    }
} finally {
    $mutex.ReleaseMutex()
    $mutex.Dispose()
}
