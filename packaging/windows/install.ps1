#Requires -RunAsAdministrator
# NextSQL zip installer. Run from the extracted archive.
param(
    [string]$InstallDir = $(Join-Path ${env:ProgramFiles} "NextSQL"),
    [string]$DataRoot = $(Join-Path ${env:ProgramData} "NextSQL"),
    [switch]$NoService,
    [switch]$NoPath
)

$ErrorActionPreference = "Stop"
$Here = Split-Path -Parent $MyInvocation.MyCommand.Path

function Need-File([string]$Name) {
    $p = Join-Path $Here $Name
    if (-not (Test-Path $p)) { throw "missing $p" }
    return $p
}

$bins = @("nextsql.exe", "nextsqld.exe", "nextsql-bench.exe")
foreach ($b in $bins) { Need-File $b | Out-Null }

Write-Host "Installing NextSQL"
Write-Host "  binaries : $InstallDir"
Write-Host "  data     : $(Join-Path $DataRoot 'data')"
Write-Host "  key file : $(Join-Path $DataRoot 'keys\root.key')"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
foreach ($b in $bins) {
    Copy-Item -Force (Join-Path $Here $b) (Join-Path $InstallDir $b)
}
foreach ($extra in @("README.txt", "COPYRIGHT", "nextsql.ico", "uninstall.ps1")) {
    $src = Join-Path $Here $extra
    if (Test-Path $src) { Copy-Item -Force $src (Join-Path $InstallDir $extra) }
}

$dataDir = Join-Path $DataRoot "data"
$keysDir = Join-Path $DataRoot "keys"
$logsDir = Join-Path $DataRoot "logs"
New-Item -ItemType Directory -Force -Path $dataDir, $keysDir, $logsDir | Out-Null

$conf = Join-Path $DataRoot "nextsql.conf"
if (-not (Test-Path $conf)) {
    $src = Join-Path $Here "nextsql.conf"
    if (Test-Path $src) {
        $text = Get-Content -Raw $src
        $text = $text -replace "data_dir=.*", ("data_dir=" + ($dataDir -replace '\\', '/'))
        $text = $text -replace "key_file=.*", ("key_file=" + ((Join-Path $keysDir 'root.key') -replace '\\', '/'))
        Set-Content -Path $conf -Value $text -Encoding Ascii
    }
}

# Restrict the keys directory to Administrators + SYSTEM.
& icacls $keysDir /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F" | Out-Null

if (-not $NoPath) {
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $parts = $machinePath -split ';' | Where-Object { $_ -ne "" }
    if ($parts -notcontains $InstallDir) {
        [Environment]::SetEnvironmentVariable("Path", ($machinePath.TrimEnd(';') + ";" + $InstallDir), "Machine")
        $env:Path += ";$InstallDir"
        Write-Host "Added $InstallDir to the machine PATH."
    }
}

$svcName = "NextSQL"
if (-not $NoService) {
    $existing = Get-Service -Name $svcName -ErrorAction SilentlyContinue
    $binPath = "`"$InstallDir\nextsqld.exe`" --config `"$conf`""
    if ($null -eq $existing) {
        New-Service -Name $svcName -BinaryPathName $binPath -DisplayName "NextSQL Database Server" `
            -StartupType Manual -Description "Encrypted-by-default multimodel database" | Out-Null
        Write-Host "Registered Windows service '$svcName' (Manual). Not started."
    } else {
        Write-Host "Service '$svcName' already exists; leaving it in place."
    }
}

$reg = "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\NextSQL"
New-Item -Path $reg -Force | Out-Null
Set-ItemProperty -Path $reg -Name DisplayName -Value "NextSQL"
Set-ItemProperty -Path $reg -Name Publisher -Value "bzync"
Set-ItemProperty -Path $reg -Name InstallLocation -Value $InstallDir
Set-ItemProperty -Path $reg -Name UninstallString -Value "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$InstallDir\uninstall.ps1`""
Set-ItemProperty -Path $reg -Name DisplayIcon -Value (Join-Path $InstallDir "nextsql.ico")
Set-ItemProperty -Path $reg -Name NoModify -Value 1 -Type DWord
Set-ItemProperty -Path $reg -Name NoRepair -Value 1 -Type DWord
if (Test-Path (Join-Path $Here "VERSION")) {
    Set-ItemProperty -Path $reg -Name DisplayVersion -Value (Get-Content -Raw (Join-Path $Here "VERSION")).Trim()
}

$programs = Join-Path $env:ProgramData "Microsoft\Windows\Start Menu\Programs\NextSQL"
New-Item -ItemType Directory -Force -Path $programs | Out-Null
$ws = New-Object -ComObject WScript.Shell
$sc = $ws.CreateShortcut((Join-Path $programs "NextSQL CLI.lnk"))
$sc.TargetPath = Join-Path $InstallDir "nextsql.exe"
$sc.WorkingDirectory = $InstallDir
$sc.Description = "NextSQL CLI"
if (Test-Path (Join-Path $InstallDir "nextsql.ico")) { $sc.IconLocation = Join-Path $InstallDir "nextsql.ico" }
$sc.Save()

Write-Host ""
Write-Host "NextSQL is installed. Initialize a data directory before starting the service:"
Write-Host "  nextsql init --data-dir `"$dataDir`" --key-file `"$(Join-Path $keysDir 'root.key')`" --user app --password-file <password-file>"
Write-Host "  Start-Service NextSQL"
Write-Host "Keep the root unlock key off the data volume in production."
