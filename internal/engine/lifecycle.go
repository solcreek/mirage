package engine

import (
	"fmt"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
)

// WaitRunning blocks until the VM is running (keeps vz state types out of
// callers that only need the common case).
func WaitRunning(vm *vz.VirtualMachine, timeout time.Duration) error {
	return WaitState(vm, vz.VirtualMachineStateRunning, timeout)
}

// StartFresh builds and boots a VM, retrying the start a few times because a
// just-stopped sibling can still hold the disk image (vz Stop is async). It
// returns once the VM is running.
func StartFresh(b bundle.Bundle, c *bundle.Config, opts Options, attempts int) (*vz.VirtualMachine, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		vm, err := BuildVM(b, c, opts)
		if err != nil {
			return nil, err
		}
		if err := vm.Start(); err != nil {
			lastErr = err
			time.Sleep(1500 * time.Millisecond)
			continue
		}
		if err := WaitRunning(vm, 2*time.Minute); err != nil {
			return nil, err
		}
		return vm, nil
	}
	return nil, lastErr
}

// WaitState blocks until the VM reaches want or the timeout elapses.
func WaitState(vm *vz.VirtualMachine, want vz.VirtualMachineState, timeout time.Duration) error {
	if vm.State() == want {
		return nil
	}
	ch := vm.StateChangedNotify()
	deadline := time.After(timeout)
	for {
		select {
		case s := <-ch:
			if s == want {
				return nil
			}
			if s == vz.VirtualMachineStateError {
				return fmt.Errorf("vm entered error state while waiting for %v", want)
			}
		case <-deadline:
			return fmt.Errorf("timeout after %s waiting for %v (now %v)", timeout, want, vm.State())
		}
	}
}

// StartGUI boots the VM and opens an interactive window. It blocks until the
// window closes (which stops the VM). This is the foreground image-prep path;
// headless lifecycle is owned by the per-VM supervisor. If recovery is true the
// VM boots into recoveryOS (used to toggle SIP for TCC seeding).
func StartGUI(vm *vz.VirtualMachine, title string, width, height float64, recovery bool) error {
	if err := vm.Start(vz.WithStartUpFromMacOSRecovery(recovery)); err != nil {
		return err
	}
	if err := WaitState(vm, vz.VirtualMachineStateRunning, 2*time.Minute); err != nil {
		return err
	}
	return vm.StartGraphicApplication(width, height, vz.WithWindowTitle(title))
}
