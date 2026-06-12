# Spike S7 — zero-touch create (offline golden-image prep)

**Goal:** turn a fresh `mirage create` (userless, boots to Setup Assistant) into
a ready golden image with **zero GUI interaction** — no Setup Assistant, no
recovery `csrutil`, no in-guest commands — by mounting the installed disk on the
host and writing everything offline.

## Approach

`scripts/zt-stage.sh` (no sudo) builds + signs the agent, computes its TCC
csreq, and generates an offline admin user (`ShadowHashData` for password
`mirage`) + kcpassword. `scripts/zt-apply.sh` (sudo) mounts the image's **Data**
volume and writes: the dslocal user record + admin-group membership, auto-login
(kcpassword + autoLoginUser), the agent + LaunchDaemon, the Screen Recording TCC
grant, and `.AppleSetupDone`. Then boot — no clicks.

## What works (verified on macOS 26.3.1)

- **Offline user creation is honored by macOS** — the hard, novel part. The
  offline-written dslocal record yields a fully-recognized account:
  `id admin` → `uid=501(admin) gid=20(staff) groups=…,80(admin),…`, and
  `dscl . -authonly admin mirage` → **success** (the offline-generated
  PBKDF2-SHA512 `ShadowHashData` validates). Setup Assistant is skipped.
- **Agent runs headless** (root LaunchDaemon, root-owned): a fresh install
  becomes a VM where `exec` / `run` / `clone` work with **zero GUI interaction**.
- All offline writes land correctly **once the image volume is remounted with
  `owners`** (disk-image volumes default to "ignore ownership", which silently
  discards root ownership — launchd refuses non-root daemons; this was the main
  pitfall).

## The wall: GUI auto-login needs SecureToken (Apple Silicon)

`screenshot` still fails on a zero-touch image: auto-login never fires
(`console=root`). Root cause: the offline user has **no SecureToken / is not a
volume owner** (`sysadminctl -secureTokenStatus admin` → DISABLED; the only APFS
cryptographic user is a different uuid). On Apple Silicon the GUI login session
is gated on volume ownership; a Setup-Assistant user gets a SecureToken
automatically, but an offline-created one does not, and granting one requires an
existing token-holder's credentials (none are known). This is a genuine
Apple-Silicon limitation, not a config error — everything else (autoLoginUser,
kcpassword decoding to `mirage`, FileVault off, valid password) is correct.

## Outcome / recommendation

- **Headless agent VMs (exec/run/clone — the core Mirage value): zero-touch
  create works.** Ship it.
- **VMs that need the GUI / screenshot:** still need the one-time manual Setup
  Assistant (which grants the SecureToken), or a future SecureToken-grant spike.
- Net: two golden-image paths — zero-touch for agent fleets, Setup-Assistant-once
  for GUI/screenshot images.

Account on zero-touch images: `admin` / `mirage`.

## Integrated into the CLI

- `mirage create <name> --ipsw <path> --headless` — installs, then runs the
  offline prep automatically (prompts once for sudo). One command → ready
  headless golden image.
- `mirage prep <name>` — runs the prep on an already-installed image.
- TCC seeding is **self-contained**: a fresh install's empty TCC.db is
  initialized from `guest/tcc-schema.sql` (the captured macOS-26 schema, version
  32) and the grant inserted — no dependency on a prepped `base`.
- `zt-apply` retries the disk attach (the just-stopped installer VM can hold the
  fd briefly), and remounts the image volume with `owners` so root ownership of
  the agent/dslocal/TCC writes persists (the key pitfall).

Open (v0.2): the prep shells out to `scripts/zt-*.sh` resolved relative to the
binary; bundling them into the binary (or porting the offline writes to Go) so a
shipped `mirage` is self-contained.
