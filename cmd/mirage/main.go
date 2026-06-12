// Command mirage is the CLI. Every subcommand honors a global --json flag that
// emits exactly one envelope on stdout at exit (human progress goes to stderr).
package main

import (
	"fmt"
	"os"
)

const usage = `mirage — ephemeral macOS VMs on Apple Silicon

usage: mirage [--json] <command> [args]

commands:
  create <name> --ipsw <path> [--disk-gb 40]   install a macOS golden image
  ls                                           list images and VMs
  clone <src> <dst>                            instant copy-on-write clone
  start <name> --gui                           boot with an interactive window
  rm <name>                                    delete a bundle
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
	ctx := &cmdContext{json: jsonOut}
	var code int
	switch cmd {
	case "create":
		code = ctx.run(cmdCreate(cmdArgs))
	case "ls":
		code = ctx.run(cmdLs(cmdArgs))
	case "clone":
		code = ctx.run(cmdClone(cmdArgs))
	case "start":
		code = ctx.run(cmdStart(cmdArgs))
	case "rm":
		code = ctx.run(cmdRm(cmdArgs))
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
