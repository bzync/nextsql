# NextSQL installers

Build scripts that package `nextsql`, `nextsqld`, and `nextsql-bench`.

```bash
# Both platforms (from a Linux host; Windows is cross-compiled)
./scripts/build-installers.sh

# Linux only (.tar.gz, .run, .deb; .rpm if rpmbuild is installed)
./scripts/build-linux-installer.sh --arch amd64,arm64

# Windows only (.zip + setup.exe; NSIS if makensis is installed)
./scripts/build-windows-installer.sh --arch amd64
```

Artifacts land in `dist/`. Checksums: `dist/SHA256SUMS.linux`, `dist/SHA256SUMS.windows`, and `dist/SHA256SUMS` after the combined script.

Requires Go 1.22+, `tar`, `gzip`, `zip`, `sha256sum`, and `python3`. Debian packages need `dpkg-deb` (and `fakeroot` when present). The Windows `.ico` is built with Python Pillow when that package is installed.

## Linux

| Artifact | What it is |
|---|---|
| `nextsql-VERSION-linux-ARCH.tar.gz` | Portable tree with `install.sh` / `uninstall.sh` |
| `nextsql-VERSION-linux-ARCH.run` | Self-extracting installer (runs `install.sh`) |
| `nextsql_VERSION_ARCH.deb` | Debian/Ubuntu package |

`install.sh` defaults to system-wide (`/usr/local`) when run as root, or `--user` (`~/.local`) otherwise. The systemd unit is installed but **not** enabled. `nextsqld` stays down until you initialize a data directory:

```bash
sudo ./nextsql-VERSION-linux-amd64.run          # or: sudo dpkg -i nextsql_*.deb
printf 'secret\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw
nextsql init --data-dir /var/lib/nextsql --key-file /etc/nextsql/root.key \
  --user app --password-file /tmp/nextsql.pw
sudo chown -R nextsql:nextsql /var/lib/nextsql
sudo chown nextsql:nextsql /etc/nextsql/root.key
sudo chmod 600 /etc/nextsql/root.key
sudo systemctl enable --now nextsql
```

Keep the root unlock key off the data volume in production. Loopback may run without TLS; any other listen address requires `tls_cert` / `tls_key` in `/etc/nextsql/nextsql.conf`.

Purging the `.deb` does **not** delete `/var/lib/nextsql` or `/etc/nextsql/root.key`.

## Windows

| Artifact | What it is |
|---|---|
| `nextsql-VERSION-windows-ARCH.zip` | Binaries + `install.ps1` / `uninstall.ps1` |
| `nextsql-VERSION-windows-ARCH-setup.exe` | Self-extracting GUI installer (UAC) |

Silent:

```text
nextsql-VERSION-windows-amd64-setup.exe /S
nextsql-VERSION-windows-amd64-setup.exe /uninstall /S
```

Default install: `%ProgramFiles%\NextSQL`. Data: `%ProgramData%\NextSQL\data`. Key: `%ProgramData%\NextSQL\keys\root.key`. The `NextSQL` service is demand-start and is not started by the installer.

```powershell
nextsql init --data-dir "$env:ProgramData\NextSQL\data" `
  --key-file "$env:ProgramData\NextSQL\keys\root.key" `
  --user app --password-file $env:TEMP\nextsql.pw
Start-Service NextSQL
```

## Layout

```text
packaging/
  lib.sh                 shared version / arch helpers
  COPYRIGHT
  linux/                 systemd unit, sysusers, tmpfiles, Debian scripts, tarball installer
  windows/               config, PowerShell, NSIS, setup/ (Go SFX stub)
scripts/
  build-linux-installer.sh
  build-windows-installer.sh
  build-installers.sh
```
