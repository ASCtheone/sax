# SAX installer for Windows
# Usage: irm https://raw.githubusercontent.com/ASCtheone/sax/main/install.ps1 | iex
$ErrorActionPreference = "Stop"

$Repo = "ASCtheone/sax"
$Binary = "sax-windows-amd64.exe"

Write-Host "Detecting platform: windows/amd64"

# Get latest release tag
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Tag = $Release.tag_name
$Asset = $Release.assets | Where-Object { $_.name -eq $Binary }
if (-not $Asset) {
    Write-Error "No binary found for windows/amd64 in release $Tag"
    exit 1
}
$Url = $Asset.browser_download_url

Write-Host "Downloading sax $Tag..."

# Install directory
$InstallDir = Join-Path $env:LOCALAPPDATA "sax\bin"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

$OutFile = Join-Path $InstallDir "sax.exe"
Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing

Write-Host "Installed sax $Tag to $OutFile"

# Add to user PATH if not already there
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    Write-Host "Added $InstallDir to user PATH (restart your terminal to pick it up)"
} else {
    Write-Host "$InstallDir is already in PATH"
}
