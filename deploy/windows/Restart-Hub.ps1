$ErrorActionPreference = 'Stop'
$taskName = 'CLIProxyAPI Personal Hub'
Stop-ScheduledTask -TaskName $taskName
$mutex = New-Object System.Threading.Mutex($false, 'Global\CLIProxyAPI-PersonalHub')
$acquired = $false
try {
    try { $acquired = $mutex.WaitOne(15000) }
    catch [System.Threading.AbandonedMutexException] { $acquired = $true }
    if (-not $acquired) { throw 'The previous hub watchdog has not stopped.' }
} finally {
    if ($acquired) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}
$deadline = (Get-Date).AddSeconds(15)
while ((Get-ScheduledTask -TaskName $taskName).State -eq 'Running') {
    if ((Get-Date) -gt $deadline) { throw 'The previous scheduled task has not stopped.' }
    Start-Sleep -Milliseconds 250
}
Start-ScheduledTask -TaskName $taskName
