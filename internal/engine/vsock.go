package engine

import (
	"fmt"
	"time"

	"github.com/Code-Hex/vz/v3"
)

// AgentPort is the guest vsock port the agent's root listener binds.
const AgentPort = 4444

// ConnectGuest opens a host→guest vsock connection to the given port. The host
// has no AF_VSOCK API; the connection is routed through the VM's
// VZVirtioSocketDevice and surfaces as a net.Conn-compatible stream.
func ConnectGuest(vm *vz.VirtualMachine, port uint32) (*vz.VirtioSocketConnection, error) {
	devs := vm.SocketDevices()
	if len(devs) == 0 {
		return nil, fmt.Errorf("vm has no virtio socket device")
	}
	return devs[0].Connect(port)
}

// DialGuest retries ConnectGuest until the guest agent is listening or the
// timeout elapses (the connect fails until the in-guest listener binds).
func DialGuest(vm *vz.VirtualMachine, port uint32, timeout time.Duration) (*vz.VirtioSocketConnection, error) {
	deadline := time.Now().Add(timeout)
	delay := 250 * time.Millisecond
	for {
		conn, err := ConnectGuest(vm, port)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("guest agent not reachable on port %d within %s: %w", port, timeout, err)
		}
		time.Sleep(delay)
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}
