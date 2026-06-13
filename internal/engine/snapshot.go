package engine

import (
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/solcreek/mirage/internal/bundle"
)

// SaveState pauses a running VM and writes its RAM/device state to path. The VM
// is left paused; the caller clones the (now quiesced) disk and then resumes or
// stops it. Pairing this file with that disk clone yields a consistent restore
// point — the saved state alone would mismatch a disk that kept changing.
func SaveState(vm *vz.VirtualMachine, path string) error {
	if err := vm.Pause(); err != nil {
		return err
	}
	return vm.SaveMachineStateToPath(path)
}

// RestoreFresh builds a VM from the bundle, restores previously saved state, and
// resumes it — returning once it is running. The caller must reset the disk to
// the snapshot's paired disk first so the restored RAM matches it. The restore
// is retried for the disk-lock race a just-stopped sibling can cause.
func RestoreFresh(b bundle.Bundle, c *bundle.Config, opts Options, statePath string, attempts int) (*vz.VirtualMachine, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		vm, err := BuildVM(b, c, opts)
		if err != nil {
			return nil, err
		}
		if err := vm.RestoreMachineStateFromURL(statePath); err != nil {
			lastErr = err
			time.Sleep(1500 * time.Millisecond)
			continue
		}
		if err := vm.Resume(); err != nil {
			return nil, err
		}
		if err := WaitRunning(vm, 2*time.Minute); err != nil {
			return nil, err
		}
		return vm, nil
	}
	return nil, lastErr
}
