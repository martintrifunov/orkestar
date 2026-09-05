# Run only against the just-built executable and an isolated runtime directory.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Executable = Join-Path (Split-Path $PSScriptRoot -Parent) 'orkestar.exe'
$PreviousRuntime = $env:ORKESTAR_RUNTIME_DIR
$RuntimeDirectory = Join-Path ([IO.Path]::GetTempPath()) ('orkestar-smoke-' + [Guid]::NewGuid().ToString('N'))
$env:ORKESTAR_RUNTIME_DIR = $RuntimeDirectory
function Invoke-Orkestar {
    param([string[]]$Arguments)
    $Output = & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Orkestar failed: $Arguments" }
    return $Output
}
$ResetCompleted = $false
try {
    # Ensure launches a detached daemon. The CLI exits before the next call.
    Invoke-Orkestar @('reset')
    Invoke-Orkestar @('status')
    $Workspace = Invoke-Orkestar @('workspace', 'create', $RuntimeDirectory)
    $WorkspaceID = ($Workspace -split '\s+')[0]
    Invoke-Orkestar @('terminal', 'start', $WorkspaceID, '--', 'powershell.exe', '-NoProfile', '-Command', 'Start-Sleep -Seconds 60')
    Invoke-Orkestar @('status')
    Invoke-Orkestar @('reset', '--yes')
    $ResetCompleted = $true
}
finally {
    # reset --yes already stopped the daemon; stop only on an earlier failure.
    if (-not $ResetCompleted) { & $Executable daemon stop }
    $env:ORKESTAR_RUNTIME_DIR = $PreviousRuntime
    # Shutdown closes SQLite asynchronously; wait for its handles before cleanup.
    for ($Attempt = 0; $Attempt -lt 50; $Attempt++) {
        try {
            if (Test-Path -LiteralPath $RuntimeDirectory) {
                Remove-Item -LiteralPath $RuntimeDirectory -Recurse -Force
            }
            break
        }
        catch {
            if ($Attempt -eq 49) { throw }
            Start-Sleep -Milliseconds 100
        }
    }
}
