# Mirage

[![CI](https://github.com/solcreek/mirage/actions/workflows/ci.yml/badge.svg)](https://github.com/solcreek/mirage/actions/workflows/ci.yml)

Ephemeral macOS virtual machines on Apple Silicon — **agent-native** and
**CLI-first**, with an [MCP](https://modelcontextprotocol.io) server and a native
SwiftUI app. Built on Apple's Virtualization.framework.

Mirage makes a macOS VM something you can spin up, drive, snapshot, and throw
away in seconds — from a shell, from an AI agent, or from a window.

## Highlights

- **Instant clones** — copy-on-write (APFS clonefile); a fresh VM in ~10 ms
  regardless of disk size.
- **Warm snapshot / restore** — freeze a running VM (memory + disk together) and
  restore straight back to the logged-in desktop, skipping the cold boot.
- **Headless agent control** — `exec` a command in a guest, or `run` a one-shot
  (clone → run → destroy). Host↔guest over a vsock channel; no SSH.
- **MCP server** — the same operations as typed tools (`vm_clone`, `vm_exec`,
  `vm_run`, `vm_snapshot`, …) so an agent can fan out across disposable VMs.
- **Native SwiftUI app** — a live VM window (host-side rendering, no in-guest
  capture), one-click snapshot/restore, and PNG export.
- **Zero-touch create** — install a golden image that boots ready with no Setup
  Assistant clicks (offline user + auto-login + guest agent).
- **CLI ⇄ API ⇄ GUI parity** — every command speaks `--json`; the GUI and MCP
  server are thin layers over the same core.

> Status: early (v0.1). macOS guests today; the core is structured so other
> guest types can follow.

## Requirements

- Apple Silicon Mac (M1 or later), macOS 14+.
- Go 1.22+ and the Xcode command-line tools (for the Swift app).
- A macOS restore image (`.ipsw`) to create the first golden image.

The host enforces Apple's limit of **2 concurrently running macOS VMs**.

## Build

```sh
make build          # builds + ad-hoc signs bin/mirage (virtualization entitlement)
make tools-image    # builds the guest agent tools image
./app/package.sh    # builds a double-clickable app/build/Mirage.app
```

## Quickstart

```sh
# Create a golden image from a restore image (zero-touch headless prep).
mirage create base --ipsw ~/Downloads/UniversalMac.ipsw --headless

# Instant clone, run a command in it, then throw it away.
mirage run base -- 'sw_vers -productVersion'

# Keep a VM warm for fast repeated commands.
mirage start base
mirage exec base -- 'uname -a'

# Freeze a warm restore point, then resume straight to it later.
mirage snapshot base
mirage start base --restore

# Capture the guest display.
mirage screenshot base -o base.png

# Manage VMs.
mirage clone base work
mirage ls
mirage stop base
mirage rm work
```

Every command accepts a global `--json` flag that emits a single stable
envelope, so the CLI is scriptable and programmatic.

## MCP server

```sh
mirage mcp        # serves the tools over stdio
```

Point an MCP-capable client at `mirage mcp` to get `vm_list`, `vm_clone`,
`vm_start`, `vm_stop`, `vm_delete`, `vm_exec`, `vm_run`, `vm_snapshot`, and
`vm_screenshot`.

## GUI

```sh
./app/package.sh && open app/build/Mirage.app
```

A native window lists your images/VMs; **Open** boots a VM in-process and shows
its live screen, with **Snapshot**, **Save PNG…**, and start/stop controls.

## Architecture

- **`internal/engine`** — the single home for all Virtualization.framework calls
  (via [`Code-Hex/vz`](https://github.com/Code-Hex/vz)): build, boot, save/restore.
- **`internal/bundle`** — the on-disk VM bundle format and XDG layout; clones via
  clonefile.
- **`internal/supervisor`** — a daemonless, helper-per-VM model: each running VM
  is one process serving a per-VM socket, with a shared 2-VM quota.
- **`cmd/mirage`** — the CLI dispatcher and the MCP server.
- **`cmd/mirage-agent`** — the in-guest agent (vsock): exec, screenshot,
  auto-login setup.
- **`app/`** — the SwiftUI app, a thin client over the same core that also owns
  the VM it displays live.

## Repository layout

```
cmd/mirage         CLI + MCP server
cmd/mirage-agent   in-guest agent (vsock)
internal/engine    Virtualization.framework wrapper
internal/bundle    bundle format + clonefile
internal/supervisor per-VM helper + quota
app/               native SwiftUI app
guest/             guest install + tools image scripts
docs/              design spikes & findings
```

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
