# Mirage — development conventions

Ephemeral macOS & Linux VMs on Apple Silicon, agent-native and CLI-first.

## Commits

- Atomic commits: one logical change per commit.
- Conventional-commit style subjects (`feat:`, `fix:`, `docs:`, `chore:`, ...).
- No `Co-Authored-By` trailers.

## Engineering principles

- CLI and programmatic API have full feature parity; every command supports
  `--json` with stable, documented output.
- Headless by default — never allocate display/GPU resources unless asked.
- Errors must be actionable: say what failed, in which VM, and what to try.
- Treat resource efficiency (RAM/disk per VM) as a feature; measure it.
