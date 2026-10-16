---
name: cross-compile
description: Build and verify OpenKitty release artifacts for Linux AMD64, Linux ARM64 edge devices, Windows, or macOS. Use for cross-platform builds, Raspberry Pi binaries, and distribution packaging.
---

# Cross Compile

Run the repository release guard before producing a distributable artifact.
Prefer the checked-in build scripts over handwritten build loops.

## Linux edge builds

From Command Prompt on Windows:

```bat
scripts\build\build_linux.bat
scripts\build\build_linux.bat arm64
```

From Bash on Linux, macOS, or Git Bash:

```bash
./scripts/build/build_linux.sh all
./scripts/build/build_linux.sh arm64
```

Both scripts build with `CGO_ENABLED=0`. The Windows batch file emits a raw
binary under `dist/`; the Bash script also verifies target metadata, writes
SHA-256 checksums, and creates installable bundles.

Use `linux/arm64` for 64-bit Raspberry Pi OS or Ubuntu on Raspberry Pi 4/5.
Do not label an untested 32-bit ARM build as supported.

Read `docs/edge-deployment.md` when installing or upgrading an edge device.

## Other manual targets

Use these only when no checked-in packaging script exists:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o openkitty_windows_amd64.exe ./cmd/openkitty
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o openkitty_darwin_amd64 ./cmd/openkitty
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -buildvcs=false -trimpath -ldflags="-s -w" -o openkitty_darwin_arm64 ./cmd/openkitty
```

Verify every artifact with `go version -m <binary>`. On the target system,
also run `<binary> --version` before installation.
