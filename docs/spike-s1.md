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

## Result (runtime): pending one manual guest-prep session

End-to-end runtime confirmation needs a guest session, which the freshly
installed base image does not yet have (it stops at Setup Assistant). This is
the one-time manual prep the plan already accounts for.

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
   mkdir -p /tmp/m && mount_virtiofs mirage /tmp/m
   /tmp/m/mirage-agent
   ```
4. The host prints `✅ S1 PASSED — guest replied: {...}` once the agent listens.

Close the window to stop the VM. The result feeds the M1 decision: on PASS, the
real agent is built on this transport; the seeded golden image will launch it as
a LaunchDaemon so no manual step remains.
