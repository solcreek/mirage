// Command mirage is the CLI. Every subcommand honors a global --json flag that
// emits exactly one envelope on stdout at exit (human progress goes to stderr).
package main

import (
	"fmt"
	"os"
	"runtime"
)

func init() {
	// AppKit (the VZ graphics window) must run on the main OS thread. Pin the
	// main goroutine to it; vz.StartGraphicApplication requires this and crashes
	// nondeterministically without it.
	runtime.LockOSThread()
}

const usage = `mirage — ephemeral macOS VMs on Apple Silicon

usage: mirage [--json] <command> [args]

commands:
  create <name> --ipsw <path> [--headless]     install a macOS golden image
  prep <name>                                  zero-touch prep an installed image (sudo)
  ls                                           list images and VMs
  clone <src> <dst>                            instant copy-on-write clone
  exec <name> -- <command...>                  run a command in the guest (headless)
  run <image> -- <command...>                  clone → run → destroy (ephemeral)
  screenshot <name> [-o out.png]               capture the guest display (PNG)
  autologin <name> [user]                      enable boot-to-desktop (password on stdin)
  start <name> [--gui]                         boot a VM (headless, or windowed)
  stop <name>                                  stop a running VM
  logs <name>                                  print a VM's supervisor log
  rm <name>                                    delete a bundle
  mcp                                          run the MCP server (stdio) for agents
  version                                      print version

global flags:
  --json    emit a single JSON envelope instead of human output
`

func main() {
	args := os.Args[1:]
	jsonOut := false
	var rest []string
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd, cmdArgs := rest[0], rest[1:]
	// __vmm (supervisor) and mcp (server) are long-running, not envelope commands.
	if cmd == "__vmm" {
		runVmm(cmdArgs)
		return
	}
	if cmd == "mcp" {
		runMCPServer()
		return
	}
	ctx := &cmdContext{json: jsonOut}
	var code int
	switch cmd {
	case "create":
		code = ctx.run(cmdCreate(cmdArgs))
	case "ls":
		code = ctx.run(cmdLs(cmdArgs))
	case "clone":
		code = ctx.run(cmdClone(cmdArgs))
	case "prep":
		code = ctx.run(cmdPrep(cmdArgs))
	case "exec":
		code = ctx.run(cmdExec(cmdArgs))
	case "run":
		code = ctx.run(cmdRun(cmdArgs))
	case "screenshot":
		code = ctx.run(cmdScreenshot(cmdArgs))
	case "autologin":
		code = ctx.run(cmdAutologin(cmdArgs))
	case "start":
		code = ctx.run(cmdStart(cmdArgs))
	case "stop":
		code = ctx.run(cmdStop(cmdArgs))
	case "logs":
		code = ctx.run(cmdLogs(cmdArgs))
	case "rm":
		code = ctx.run(cmdRm(cmdArgs))
	case "__vsock-probe":
		code = ctx.run(cmdVsockProbe(cmdArgs))
	case "version":
		code = ctx.run(map[string]string{"version": version}, nil)
	case "help", "-h", "--help":
		fmt.Fprint(os.Stderr, usage)
		code = 0
	default:
		fmt.Fprintf(os.Stderr, "mirage: unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}
