# Windows personal hub

The runtime lives outside Git in `C:\Tools\cliproxyapi`. `hub.yaml` contains
private keys; `hub-auth` contains OAuth grants. Neither belongs in this repository.

`Run-Hub.ps1` holds a process mutex and restarts the hub five seconds after exit.
Run it with a hidden PowerShell window from a per-user logon task. A locked desktop
keeps running. Starting before any Windows sign-in requires a machine-level service
or scheduled task configured by an administrator.

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
