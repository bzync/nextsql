#Requires -RunAsAdministrator
# NextSQL uninstaller. Does not delete data directories or key files unless -RemoveData is passed.
param(
    [string]$InstallDir = $(Join-Path ${env:ProgramFiles} "NextSQL"),
    [string]$DataRoot = $(Join-Path ${env:ProgramData} "NextSQL"),
    [switch]$RemoveData
)

$ErrorActionPreference = "Continue"
$svcName = "NextSQL"

$svc = Get-Service -Name $svcName -ErrorAction SilentlyContinue
if ($null -ne $svc) {
    if ($svc.Status -eq "Running") { Stop-Service -Name $svcName -Force -ErrorAction SilentlyContinue }
    # Prefer sc.exe so we work even if the module cmdlet is missing.
    & sc.exe delete $svcName | Out-Null
}

$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($machinePath) {
    $parts = $machinePath -split ';' | Where-Object { $_ -ne "" -and $_.TrimEnd('\') -ne $InstallDir.TrimEnd('\') }
    [Environment]::SetEnvironmentVariable("Path", ($parts -join ';'), "Machine")
}

Remove-Item -Recurse -Force "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL" -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force (Join-Path $env:ProgramData "Microsoft\Windows\Start Menu\Programs\NextSQL") -ErrorAction SilentlyContinue

if (Test-Path $InstallDir) {
    Remove-Item -Recurse -Force $InstallDir -ErrorAction SilentlyContinue
}

if ($RemoveData -and (Test-Path $DataRoot)) {
    Remove-Item -Recurse -Force $DataRoot
    Write-Host "Removed $DataRoot"
} else {
    Write-Host "Left data and keys in place under $DataRoot"
}

Write-Host "NextSQL uninstalled."
