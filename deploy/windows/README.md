# Windows personal hub

The runtime lives outside Git in `C:\Tools\cliproxyapi`. `hub.yaml` contains
private keys; `hub-auth` contains OAuth grants. Neither belongs in this repository.

`Run-Hub.ps1` holds a process mutex and restarts the hub five seconds after exit.
Run it through the console-free `Hub-Background.exe` launcher from a per-user logon task. A locked desktop
keeps running. Starting before any Windows sign-in requires a machine-level service
or scheduled task configured by an administrator.

Do not use `powershell.exe -WindowStyle Hidden` directly as the task action.
That configuration was observed attaching to Windows Terminal; closing the
window terminated the watchdog with exit `0xC000013A`.

Build the launcher as a Windows application with the installed .NET Framework
compiler, then set the task action to the resulting executable:

```powershell
Copy-Item .\deploy\windows\Hub-Background.cs C:\Tools\cliproxyapi\Hub-Background.cs
& "$env:WINDIR\Microsoft.NET\Framework64\v4.0.30319\csc.exe" /nologo /target:winexe /out:C:\Tools\cliproxyapi\Hub-Background.exe C:\Tools\cliproxyapi\Hub-Background.cs
$action = New-ScheduledTaskAction -Execute 'C:\Tools\cliproxyapi\Hub-Background.exe' -Argument '"C:\Tools\cliproxyapi"' -WorkingDirectory 'C:\Tools\cliproxyapi'
Set-ScheduledTask -TaskName 'CLIProxyAPI Personal Hub' -Action $action
```

The launcher starts PowerShell with `CreateNoWindow`, waits for the watchdog,
and returns its exit status to Task Scheduler. A kill-on-close Windows job
contains the launcher and its descendants so stopping the task also stops
the watchdog and backend. Launcher errors go to `background-launcher.log`.
The normal proxy log remains `hub-YYYY-MM-DD.log`.

Stop the task before replacing an in-use launcher executable. Preserve the
existing task settings and export its XML before changing the action. Verify
that task stop leaves no backend, task restart restores the listener, and no
Windows Terminal window is needed. On the first migration from the old
console action, an old backend may survive its watchdog; identify its exact
PID/path before terminating that orphan.

Use `Restart-Hub.ps1` to restart the registered task. It waits for the old watchdog
to release its mutex before starting a replacement, avoiding Task Scheduler's
ignore-new-instance race during shutdown.

The server listens on loopback port 8317. Tailscale Serve publishes that endpoint
privately as HTTPS on port 8317. Existing Serve endpoints are preserved.

`routing.strategy: soonest-reset` selects among the highest-priority ready accounts.
An existing affinity binding takes precedence. New conversations prefer the earliest
known weekly reset. Accounts without a usable reset receive an ordinary request to
discover their quota, at most once per hour, then serve as round-robin fallback when
no known reset is available. No background inference is generated. This strategy
uses passive response headers and may not know reset times immediately after restart.

Dashboard automatic updates are disabled in the private hub config. Install the
CPAMC fork's `dist/index.html` as `static/management.html` after its verify gate passes.

To roll back, stop the logon task/watchdog first, then restore the backed-up binary
and configuration. The original `config.yaml` and original credential directory
remain separate from the hub configuration and selected hub accounts.
