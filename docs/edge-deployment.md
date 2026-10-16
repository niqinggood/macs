# OpenKitty Linux edge deployment

OpenKitty can be cross-compiled as a self-contained Linux binary for 64-bit
x86 devices and 64-bit ARM devices such as Raspberry Pi 4 or Raspberry Pi 5.
The Web UI, built-in skills, default configuration template, and extraction
helpers are embedded in the executable. Optional hardware and media features
can still require operating-system packages such as FFmpeg.

## Supported targets

| Device architecture | Go target | Artifact |
|---|---|---|
| x86-64 | `linux/amd64` | `openkitty-edge_linux_amd64.tar.gz` |
| ARM64 / AArch64 | `linux/arm64` | `openkitty-edge_linux_arm64.tar.gz` |

The first edge release does not support 32-bit ARM. Use a 64-bit Raspberry Pi
OS or Ubuntu image on Raspberry Pi hardware.

## Build on Windows

From Command Prompt (`cmd.exe`), build a raw Linux binary:

```bat
scripts\build\build_linux.bat
scripts\build\build_linux.bat amd64
scripts\build\build_linux.bat arm64
```

The default target is `linux/amd64`. The binary is written to `dist/`.

## Build on Linux or macOS

```bash
./scripts/build/build_linux.sh all
./scripts/build/build_linux.sh arm64
```

The Bash packaging script writes the following artifacts to `dist/`:

- a raw `openkitty_linux_<architecture>` binary;
- a matching `.sha256` file;
- an unpacked `linux-<architecture>/` bundle;
- an `openkitty-edge_linux_<architecture>.tar.gz` installation bundle.

Build metadata can be supplied through `OPENKITTY_VERSION` and
`OPENKITTY_BUILD_DATE`. Otherwise the scripts use the current Git description
and UTC build time.

## Install on an edge device

Copy the matching archive to the device, then run:

```bash
tar -xzf openkitty-edge_linux_arm64.tar.gz
sudo bash install.sh
```

To install an existing configuration in the same operation:

```bash
sudo bash install.sh --config /path/to/openkitty-config.yaml
```

The installer verifies the SHA-256 digest, ELF format, target architecture,
and `openkitty --version` before replacing an existing installation. Upgrades
retain the previous executable at `/opt/openkitty/openkitty.previous` and
restore it automatically when the new service cannot start.

Installed paths:

| Purpose | Path |
|---|---|
| Executable | `/opt/openkitty/openkitty` |
| Application configuration | `/etc/openkitty/openkitty-config.yaml` |
| Edge runtime environment | `/etc/openkitty/openkitty.env` |
| Workspace and SQLite state | `/var/lib/openkitty` |
| systemd unit | `/etc/systemd/system/openkitty.service` |

The service runs as the unprivileged `openkitty` user. During installation the
user is added only to hardware groups already present on the device, such as
`gpio`, `i2c`, `spi`, `dialout`, and `video`.

## Verify the service

```bash
/opt/openkitty/openkitty --version
systemctl status openkitty
curl -fsS http://127.0.0.1:8080/livez
curl -fsS http://127.0.0.1:8080/readyz
journalctl -u openkitty -f
```

The edge profile listens on `127.0.0.1:8080` by default. To expose OpenKitty
on a LAN, edit `/etc/openkitty/openkitty.env` and configure API authentication
in `/etc/openkitty/openkitty-config.yaml` before changing the listen address to
`0.0.0.0:8080`. Do not expose an unauthenticated edge instance to a network.

## Resource profile

The default edge service limits OpenKitty to two concurrent AgentOS tasks, a
queue of twenty tasks, two CPU cores, and 2 GiB of memory. Adjust the runtime
environment and systemd unit for smaller devices. Full local language models
are optional; an edge instance can use a remote model API while keeping device
execution local.

## Browser fallback

Ordinary search and static-page extraction continue to use `web_fetch` over
HTTP and do not require a browser. OpenKitty starts a browser only when a task
actually needs JavaScript rendering, interaction, screenshots, or visual QA.

For those dynamic-page tasks, OpenKitty uses this order:

1. `OPENKITTY_CHROME_PATH`, when explicitly configured;
2. an installed Google Chrome, Chromium, or Microsoft Edge;
3. a previously downloaded workspace-managed browser;
4. a Stable Chrome for Testing download stored under
   `<workspace>/.openkitty/runtime/browser/`.

The managed download is serialized across OpenKitty processes, extracted with
archive traversal checks, and reused without another network request. Set
`OPENKITTY_BROWSER_AUTO_INSTALL=0` to forbid new browser downloads, or
`OPENKITTY_BROWSER_CACHE_DIR` to move the cache. An existing managed browser
remains usable when new downloads are disabled.

Google does not publish Chrome for Testing for Linux ARM64. On that target,
run a browser on another trusted machine and configure
`OPENKITTY_CDP_HOST`/`OPENKITTY_CDP_PORT`, or set
`OPENKITTY_CHROME_PATH` to a compatible locally installed Chromium build.
Keep a remote CDP endpoint on a private network; do not expose it directly to
the public internet.

The Linux binary includes a disabled-by-default robot safety runtime with a
simulation driver, bounded motion tools, watchdog, idempotency, audit log, and
strict short-command parsing. It does not drive physical motors until a
hardware-specific driver is implemented and both global and device motion
gates are explicitly enabled. See
[robot-control-architecture.md](robot-control-architecture.md).
