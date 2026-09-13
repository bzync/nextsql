# NextSQL installers

Build scripts that package `nextsql`, `nextsqld`, `nextsql-bench`, and
`nextsql-admin`.

For first-run configuration and database initialization — hardware detection,
resource presets, secure-default `nextsql.conf` generation, and post-install
health verification, all non-interactive with machine-readable output — use
`nextsql setup` (see `docs/install.md`). The OS installers and, later, the
GUI installer drive that same command; every option they expose is available
to a script.

NextSQL ships Linux packages only. Native Windows is not supported; on a
Windows machine, install the Linux packages inside WSL 2 (see
[Windows (WSL 2)](#windows-wsl-2)).

```bash
# All release artifacts
./scripts/build-installers.sh

# Linux directly (.tar.gz, .run, .deb; .rpm if rpmbuild is installed)
./scripts/build-linux-installer.sh --arch amd64,arm64
```

Artifacts land in the gitignored `installers/` directory. They are disposable
build output: do not commit them. Checksums are
`installers/SHA256SUMS.linux`, and `installers/SHA256SUMS` after the combined
script.

For a release, push a version tag only after the gates in `RELEASING.md` are
green. `.github/workflows/release-installers.yml` builds into runner-temporary
storage, verifies the payload, and uploads it to the corresponding GitHub
Release. GitHub Releases—not this repository—is the binary distribution store.

To detached-sign the checksums file with GPG, pass `--gpg-key ID` (or set
`NEXTSQL_RELEASE_GPG_KEY`) — no signing key exists in this repo/CI yet, so
this is opt-in and produces no `.asc` file unless a key is given:

```bash
./scripts/build-linux-installer.sh --gpg-key you@example.com
gpg --verify installers/SHA256SUMS.linux.asc installers/SHA256SUMS.linux
```

Requires Go 1.22+, `tar`, `gzip`, `zip`, `sha256sum`, and `python3`. Debian packages need `dpkg-deb` (and `fakeroot` when present).

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

## Windows (WSL 2)

There is no Windows installer. Install a WSL 2 distribution (for example
`wsl --install -d Ubuntu`) and use the Linux artifacts above inside it. WSL 1
is not supported. The project has not yet execution-tested this path on a
Windows host.

- Keep `--data-dir` and the key files on the distribution's own Linux
  filesystem (for example under `/var/lib/nextsql`), never under `/mnt/c` or
  another mounted Windows drive: those are 9p mounts whose fsync and file
  locking are not a durability boundary. `nextsql setup` warns when it detects
  one.
- The systemd unit needs systemd enabled in the distribution
  (`[boot] systemd=true` in `/etc/wsl.conf`, then `wsl --shutdown`). Without
  it, run `nextsqld` in the foreground.
- WSL 2 forwards loopback, so a `127.0.0.1` listener and `nextsql-admin` are
  reachable from the Windows host's browser. Client drivers on Windows connect
  to it like any other address.

## Layout

```text
packaging/
  lib.sh                 shared version / arch helpers
  COPYRIGHT
  linux/                 systemd unit, sysusers, tmpfiles, Debian scripts, tarball installer
scripts/
  build-linux-installer.sh
  build-installers.sh
```
