#!/usr/bin/env pwsh
# berrypick installer for Windows.
#
#   irm https://raw.githubusercontent.com/fverse/berrypick/main/install.ps1 | iex
#
# Environment variables:
#   VERSION      release tag to install (default: latest)
#   INSTALL_DIR  where to put the binary (default: %LOCALAPPDATA%\Programs\berrypick)
$ErrorActionPreference = 'Stop'

$repo = 'fverse/berrypick'
$binary = 'berrypick'

# Detect architecture.
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }

# Resolve the version (latest by default).
$version = $env:VERSION
if (-not $version) {
	$version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
}
if (-not $version) {
	throw 'berrypick: could not determine the latest version'
}

$asset = "${binary}_${version}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$version/$asset"

$installDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { "$env:LOCALAPPDATA\Programs\berrypick" }
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

Write-Host "Installing $binary $version (windows/$arch)..."

$zip = Join-Path $env:TEMP "$asset"
Invoke-WebRequest -Uri $url -OutFile $zip
Expand-Archive -Path $zip -DestinationPath $installDir -Force
Remove-Item $zip -ErrorAction SilentlyContinue

Write-Host "Installed $binary to $installDir\$binary.exe"

# Add the install dir to the user PATH if it isn't already there.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$installDir*") {
	[Environment]::SetEnvironmentVariable('Path', "$userPath;$installDir", 'User')
	Write-Host "Added $installDir to your PATH. Restart your shell to use it."
}
