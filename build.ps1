[CmdletBinding()]
param(
    [ValidateSet("Build", "Test", "Install", "Uninstall")]
    [string]$Task = "Build",
    [string]$InstallDir = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$RepositoryRoot = $PSScriptRoot
$IsWindowsPlatform = [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    if (-not $IsWindowsPlatform) {
        $InstallDir = Join-Path $RepositoryRoot "bin"
    }
    elseif ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $InstallDir = Join-Path $env:USERPROFILE "AppData\Local\Programs\orkestar"
    }
    else {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\orkestar"
    }
}

function Assert-Go {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "Go is not installed or is not available on PATH."
    }
}

function Invoke-Go {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    & go @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "go $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Build-Orkestar {
    param([Parameter(Mandatory = $true)][string]$OutputPath)

    Assert-Go
    $ParentDirectory = Split-Path -Parent $OutputPath
    if (-not [string]::IsNullOrWhiteSpace($ParentDirectory)) {
        New-Item -ItemType Directory -Force -Path $ParentDirectory | Out-Null
    }
    Invoke-Go -Arguments @("build", "-trimpath", "-o", $OutputPath, "./cmd/orkestar")
    Write-Host "Built $OutputPath"
}

function Add-UserPath {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $NormalizedDirectory = [System.IO.Path]::GetFullPath($Directory).TrimEnd([System.IO.Path]::DirectorySeparatorChar)
    $UserPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
    $Entries = @($UserPath -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    $AlreadyPresent = $Entries | Where-Object {
        [string]::Equals(
            [System.IO.Path]::GetFullPath($_).TrimEnd([System.IO.Path]::DirectorySeparatorChar),
            $NormalizedDirectory,
            [System.StringComparison]::OrdinalIgnoreCase
        )
    }
    if (-not $AlreadyPresent) {
        [System.Environment]::SetEnvironmentVariable("Path", (($Entries + $NormalizedDirectory) -join ";"), "User")
        Write-Host "Added $NormalizedDirectory to the user PATH."
    }

    $ProcessEntries = @($env:Path -split ";")
    if (-not ($ProcessEntries | Where-Object { [string]::Equals($_, $NormalizedDirectory, [System.StringComparison]::OrdinalIgnoreCase) })) {
        $env:Path = "$env:Path;$NormalizedDirectory"
    }
}

function Remove-UserPath {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $NormalizedDirectory = [System.IO.Path]::GetFullPath($Directory).TrimEnd([System.IO.Path]::DirectorySeparatorChar)
    $UserPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
    $Entries = @($UserPath -split ";" | Where-Object {
        if ([string]::IsNullOrWhiteSpace($_)) {
            return $false
        }
        return -not [string]::Equals(
            [System.IO.Path]::GetFullPath($_).TrimEnd([System.IO.Path]::DirectorySeparatorChar),
            $NormalizedDirectory,
            [System.StringComparison]::OrdinalIgnoreCase
        )
    })
    [System.Environment]::SetEnvironmentVariable("Path", ($Entries -join ";"), "User")
}

Push-Location $RepositoryRoot
try {
    switch ($Task) {
        "Build" {
            $OutputName = if ($IsWindowsPlatform) { "orkestar.exe" } else { "orkestar" }
            Build-Orkestar -OutputPath (Join-Path $RepositoryRoot $OutputName)
        }
        "Test" {
            Assert-Go
            if ($IsWindowsPlatform) {
                Invoke-Go -Arguments @("test", "./...", "-run", "TestWindows|TestResetStops|TestEncodeKey", "-timeout=120s")
                Invoke-Go -Arguments @("test", "./internal/store", "./internal/workflow", "./internal/terminal", "./internal/syntax")
            }
            else {
                Invoke-Go -Arguments @("test", "./...")
            }
            Invoke-Go -Arguments @("vet", "./...")
            Write-Host "Tests and vet passed."
        }
        "Install" {
            if (-not $IsWindowsPlatform) {
                throw "The Install task is intended for Windows. Use 'make install' on macOS or Linux."
            }
            $Target = Join-Path $InstallDir "orkestar.exe"
            Build-Orkestar -OutputPath $Target
            Add-UserPath -Directory $InstallDir
            Write-Host "Installed orkestar to $Target"
            Write-Host "Open a new PowerShell window, then run: orkestar"
        }
        "Uninstall" {
            if (-not $IsWindowsPlatform) {
                throw "The Uninstall task is intended for Windows."
            }
            $Target = Join-Path $InstallDir "orkestar.exe"
            if (Test-Path -LiteralPath $Target -PathType Leaf) {
                Remove-Item -LiteralPath $Target -Force
                Write-Host "Removed $Target"
            }
            Remove-UserPath -Directory $InstallDir
            if ((Test-Path -LiteralPath $InstallDir -PathType Container) -and -not (Get-ChildItem -LiteralPath $InstallDir -Force)) {
                Remove-Item -LiteralPath $InstallDir -Force
            }
            Write-Host "Removed orkestar from the user PATH."
        }
    }
}
finally {
    Pop-Location
}
