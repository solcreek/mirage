# Spike S1 — Go AF_VSOCK on a macOS guest

**Question:** can a Go binary inside a macOS guest listen on AF_VSOCK, and can
the host reach it via `VZVirtioSocketDevice`? The plan budgeted a 2–3 day
fallback (hand-rolled darwin syscalls) in case the Go ecosystem lacked support.

## Result (code level): PASS — no fallback needed

`golang.org/x/sys/unix` has **first-class darwin AF_VSOCK support** on
darwin/arm64 — not stubs:

- `AF_VSOCK = 0x28`, `VMADDR_CID_ANY/HOST/HYPERVISOR`, `IOCTL_VM_SOCKETS_GET_LOCAL_CID` in `zerrors_darwin_arm64.go`.
- `SockaddrVM` / `RawSockaddrVM` / `SizeofSockaddrVM` with a real `sockaddr()` that emits the `AF_VSOCK` family, plus `AF_VSOCK` parsing in `anyToSockaddr`.

The guest agent (`cmd/mirage-agent`) compiles to a Mach-O arm64 binary using
`unix.Socket(AF_VSOCK, SOCK_STREAM, 0)` → `unix.Bind(&unix.SockaddrVM{...})` →
`unix.Listen` → `unix.Accept`. The host side (`internal/engine/vsock.go`) uses
`Code-Hex/vz` `VirtioSocketDevice.Connect(port)`, which returns a `net.Conn`.
**The 2–3 day hand-rolled-syscall fallback is cancelled.**

## Result (runtime): PASS (2026-06-12)

Confirmed end to end on a macOS 26.3.1 guest. The host connected to the guest's
AF_VSOCK listener via `VZVirtioSocketDevice.Connect(4444)`, sent `ping`, and the
guest replied over vsock:

```
✅ host→guest vsock connection established
✅ S1 PASSED — guest replied over vsock:
   {"agent":"mirage-agent","guest":{"hostname":"Lawrences-Virtual-Machine.local","product_ver":"26.3.1"},"ok":true}
```

So the full transport works: Go-on-darwin AF_VSOCK listen + host
`VZVirtioSocketDevice` connect + request/response round-trip. M1 can build the
real agent on this transport.

### Gotcha found and fixed

`vz.StartGraphicApplication` **requires `runtime.LockOSThread()`** on the calling
(main) goroutine; without it the graphics window crashes nondeterministically
with SIGTRAP in `startVirtualMachineWindow` (works once, then fails after the
goroutine migrates off the main OS thread). Fixed in `cmd/mirage` `init()`.

### Notes for the real agent (M1)

- `mount_virtiofs <tag> <dir>` mounts the VirtioFS share in the guest; binary
  staged via the share runs fine (ad-hoc signed, not quarantined).
- Setup Assistant is still a one-time manual step (no automation yet — see the
  zero-touch-create goal). Once the account exists it persists on disk across
  reboots; only the live session is lost on stop.

### Runbook

1. Build + stage (already done; re-run to refresh):
   ```
   make build
   GOOS=darwin GOARCH=arm64 go build -o ~/.local/share/mirage/spike-share/mirage-agent ./cmd/mirage-agent
   codesign -s - --force ~/.local/share/mirage/spike-share/mirage-agent
   ```
2. Boot the probe (opens a window; the host polls for the agent in the background):
   ```
   ./bin/mirage __vsock-probe base ~/.local/share/mirage/spike-share
   ```
3. In the guest window, complete Setup Assistant once (create a user). Then open
   Terminal and run:
   ```
   sudo mkdir -p /tmp/m && sudo mount_virtiofs mirage /tmp/m
   /tmp/m/mirage-agent
   ```
4. The host prints `✅ S1 PASSED — guest replied: {...}` once the agent listens
   (also written to `/tmp/mirage-s1-result.txt`).

Close the window to stop the VM. On PASS (now confirmed), the real agent is built
on this transport; the seeded golden image will launch it as a LaunchDaemon so no
manual step remains.
