NextSQL for Windows
===================

Encrypted-by-default multimodel database.

  nextsql.exe        CLI (init, exec, migrate, backup, ...)
  nextsqld.exe       Server (NSQL v1 protocol, default 127.0.0.1:7210)
  nextsql-bench.exe  Official measurements (encryption, WAL, fsync on)

GUI installer
-------------
Run NextSQL-*-setup.exe as Administrator. It copies binaries to
"%ProgramFiles%\NextSQL", writes config under "%ProgramData%\NextSQL",
adds the install directory to PATH, and registers a demand-start
Windows service named NextSQL. The service is not started until you
initialize a data directory.

Silent:

  NextSQL-0.1.0-dev-windows-amd64-setup.exe /S
  NextSQL-0.1.0-dev-windows-amd64-setup.exe /uninstall /S

Zip / PowerShell
----------------
Extract the zip and, from an elevated PowerShell:

  Set-ExecutionPolicy -Scope Process Bypass
  .\install.ps1
  .\uninstall.ps1

Initialize
----------
Keep the root unlock key off the data volume in production.

  printf 'secret\n' | Out-File -Encoding ascii -NoNewline $env:TEMP\nextsql.pw
  nextsql init --data-dir "$env:ProgramData\NextSQL\data" --key-file "$env:ProgramData\NextSQL\keys\root.key" --user app --password-file $env:TEMP\nextsql.pw
  Start-Service NextSQL

Loopback (127.0.0.1:7210) may run without TLS. Any other listen address
requires tls_cert and tls_key in nextsql.conf.

https://github.com/bzync/nextsql
