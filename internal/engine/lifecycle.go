package engine

import (
	"fmt"
	"time"

	"github.com/Code-Hex/vz/v3"
)

// WaitRunning blocks until the VM is running (keeps vz state types out of
// callers that only need the common case).
func WaitRunning(vm *vz.VirtualMachine, timeout time.Duration) error {
	return WaitState(vm, vz.VirtualMachineStateRunning, timeout)
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
// headless lifecycle is owned by the per-VM supervisor.
func StartGUI(vm *vz.VirtualMachine, title string, width, height float64) error {
	if err := vm.Start(); err != nil {
		return err
	}
	if err := WaitState(vm, vz.VirtualMachineStateRunning, 2*time.Minute); err != nil {
		return err
	}
	return vm.StartGraphicApplication(width, height, vz.WithWindowTitle(title))
}
