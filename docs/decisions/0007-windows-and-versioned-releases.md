# ADR 0007: Windows runtime and versioned distribution

- Status: accepted
- Date: 2026-09-05
- Extends ADR 0001 and ADR 0003 at the user's request

Version releases with semantic tags, beginning at v0.1.0. The executable exposes
`--version` without resolving runtime paths or contacting a daemon. Release
builds stamp `main.version`. GitHub releases produce archives and SHA-256 checksums
for macOS, Linux and Windows, each on amd64 and arm64.

Follow the maintainer's thisyou distribution pattern: a source-built Homebrew
formula in martintrifunov/homebrew-tap and a PowerShell installer that checks the
download before installing into the user's profile and adding it to PATH.
There is no WinGet integration. Formula generation uses the published tag's real
source archive checksum; no placeholder formula is installed into the tap.

Windows uses the existing xpty dependency's ConPTY implementation, a detached
background daemon, and Microsoft go-winio named pipes behind internal/ipc. The
pipe name is derived from the runtime locator; the locator remains a filesystem
path for metadata and hook compatibility. Its ACL grants access only to the
current user. Unix sockets and their permissions remain unchanged. No TCP
listener or web interface is added. The default Windows shell is PowerShell 7
when available, otherwise Windows PowerShell. Direct CLI attachments poll size;
the TUI uses its existing window events. Batch launchers, including npm agent
shims, run through cmd.exe with command and argument escaping.

ConPTY synchronous writes are cancelled on their owning OS thread after the
write deadline. Cancellation finishes before that thread returns to Go. Explicit
shutdown targets the process tree with bounded taskkill calls. After process
exit, ConPTY closes while the output reader drains its final screen. PTY packages
remain independent of domain code and UI dependencies.

Windows requires Windows 10 1809 or later (or Windows 11) and a terminal with VT
support. CI has native PowerShell/ConPTY reconnect, reset, final-output and blocked
input checks; POSIX-shell fixtures remain on macOS/Linux. Cross-compilation alone
does not validate Windows behavior. Authenticated installed-agent validation,
including native hook shell conventions on Windows, remains separate.

Replacing a binary alone never replaces a running daemon. At the user's explicit
request, `reset --yes` stops the old daemon, waits for process exit, starts the
current binary to clear persisted state, and stops it again. The OS identifies
the IPC peer PID (Unix peer credentials or Windows named-pipe server identity),
so old daemons do not need a new capability or PID method. Waiting only for socket
closure is insufficient: the old daemon still has child cleanup and a final
SQLite write to complete. Preview and ordinary connection never stop a daemon.
