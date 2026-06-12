# Spike S3 — guest screenshot via the GUI agent + TCC

**Question:** can the user-session LaunchAgent capture the guest display with
`screencapture`, and what does macOS 26 require TCC-wise (Screen Recording)?
The plan assumed TCC.db seeding via SIP-off recovery; this spike checks what is
actually needed before building that.

## What's built

- Guest agent gains a `serve-gui` mode (vsock **:4445**) that handles
  `screenshot` by running `screencapture -x -t png` and returning base64 PNG.
- Installed as a **user LaunchAgent** (`com.solcreek.mirage-agent-gui`) so it
  runs in the logged-in GUI session; the root daemon (:4444) cannot capture.
- Host: `engine.AgentScreenshot` (:4445) → supervisor `OpScreenshot` →
  `mirage screenshot <name>` CLI + `vm_screenshot` MCP tool (returns the image).

## Runbook

1. Rebuild + tools image: `make build && make tools-image`
2. Prep the image (GUI session, once):
   ```
   ./bin/mirage start base --gui --tools bin/mirage-tools.img
   ```
   In the guest:
   ```
   sudo /Volumes/MirageTools/install.sh                       # daemon :4444 + gui agent :4445
   sudo /Volumes/MirageTools/mirage-agent setup-autologin admin  # GUI session on headless boot
   ```
3. Close the window. Then:
   ```
   ./bin/mirage start base          # headless; auto-login brings up the GUI session
   ./bin/mirage screenshot base -o /tmp/shot.png
   ./bin/mirage stop base
   open /tmp/shot.png
   ```

## What to observe (drives the TCC strategy)

- **Real desktop image** → screenshot works with no TCC grant on macOS 26; done.
- **Solid black / blank image** → `screencapture` ran but Screen Recording is not
  granted; need to grant it (System Settings → Privacy → Screen Recording) or
  seed TCC.db.
- **Error from the agent** (`screencapture: ...`) → no GUI session (auto-login
  didn't take) or a harder failure.

Record the outcome here. If a grant is needed, the next step is to decide
between a one-time manual grant in the golden image (clone-inherits) vs.
automated TCC.db seeding via SIP-off recovery boot.

## Result (2026-06-12): TCC Screen Recording IS required

First run returned: `screencapture: exit status 1: could not create image from
display`. The whole path works — the GUI LaunchAgent runs in the auto-login
session on :4445, the host reaches it, `screencapture` executes — but macOS
denies the capture because the calling process lacks **Screen Recording (TCC)**
permission. This is the expected macOS-26 behavior; there is no interactive
prompt in an automated/headless context, so it just fails.

So a grant is needed. The agent is a bare CLI binary (`/usr/local/bin/mirage-agent`),
not a `.app`, which is awkward to grant via System Settings — pointing to the
real solution: **seed the system TCC.db** (`/Library/Application Support/com.apple.TCC/TCC.db`)
with a `kTCCServiceScreenCapture` row for the agent, done once during golden-image
prep via a SIP-off recovery boot. Clones then inherit the grant.

## Resolution (2026-06-12): screenshot works

The working recipe, validated end to end (captured a real 1920×1080 desktop PNG):

1. **SIP off** via recovery boot (`mirage start <img> --recovery` → recovery
   Terminal → `csrutil disable` → reboot). Confirmed `csrutil status: disabled`.
2. **Grant mirage-agent ScreenCapture** in the *system* TCC.db
   (`/Library/Application Support/com.apple.TCC/TCC.db`): row with
   `service=kTCCServiceScreenCapture`, `client=/usr/local/bin/mirage-agent`,
   `client_type=1`, `auth_value=2`, and a `csreq` pinned to the agent's 20-byte
   cdhash (`cdhash H"<CDHash>"` → `csreq -r- -b`). macOS-26 `access` schema
   recorded; explicit-column INSERT works. `killall tccd` to refresh.
3. **Capture from the root daemon via `launchctl asuser <console-uid>
   screencapture`** — screencapture only sees the display inside the Aqua
   session, and only root can `asuser`. TCC attributes the request to the
   responsible process = **mirage-agent** (granting screencapture itself does
   nothing; granting mirage-agent is load-bearing — proven by removing each row).
4. **Unique temp path per capture** — a fixed `/tmp` path leaves a stale
   root-owned file that the sticky bit makes unwritable next time.

Architecture change: screenshot is served by the root daemon on **:4444**; the
separate GUI LaunchAgent (:4445) was removed.

### The consent dialog — cracked

The "bypass the private window picker / directly access your screen" dialog is
macOS's **periodic screen-recording reminder**, and it is stored on the *same*
`kTCCServiceScreenCapture` row, not a separate store. Found by before/after diff
of a real "Allow": clicking Allow writes the row with **`auth_reason=4`** and
**`last_reminded=<now>`**. Our seed used `auth_reason=2` + `last_reminded=0`, so
macOS still showed the reminder even though `auth_value=2` let the capture
through. Fix (in `seed-tcc.sh`): seed the row with `auth_reason=4` and
`last_reminded=strftime('%s','now')` — mirrors a real Allow, no dialog.

**Write-window constraint (important):** the `com.apple.TCC` directory is only
writable during the prep window — SIP off **and** before `tccd` re-locks the dir
this boot. Once `tccd` has claimed it, even root gets `Permission denied` /
sqlite `readonly` (booting out `tccd` is also "Operation not permitted"). So ALL
TCC seeding must happen in a single `seed-tcc.sh` pass right after `csrutil
disable`, before sealing. A late update to a long-running image cannot modify
TCC.db.

### Durability — solved with a stable signing identity

`scripts/dev-agent-cert.sh` creates a self-signed code-signing identity; the
build signs the agent with it (`--identifier com.solcreek.mirage-agent`), giving
a stable designated requirement `identifier "com.solcreek.mirage-agent" and
certificate leaf = H"…"`. `seed-tcc.sh` derives the TCC csreq from that, so the
Screen Recording grant matches by **identifier+cert, not cdhash** — it survives
agent rebuilds **and** clones (whose cdhash differs).

### Verified end to end (2026-06-12)

After a proper prep pass (`start base --gui --tools …` → `install.sh` →
`seed-tcc.sh`, SIP off): `screenshot base` returns a real 1920×1080 PNG with
**no consent dialog**; a fresh **clone** screenshots cleanly too (stable csreq
matches its different cdhash); `exec`/`run` intact. Both deliverables confirmed.

### Known follow-ups

- **Screenshot needs the GUI (Aqua) session up**, which lags the root agent by
  ~30 s after a fresh clone boot; `screenshot` right after `start` can fail with
  "could not create image" until auto-login completes. `captureScreen` should
  wait/retry for a console user. (Because the grant is cert-pinned, this agent
  fix can ship without re-seeding.)
- **Enforced sealing** (read-only golden image, clone-only, with a clear error
  on direct boot) is a v0.2 item — read-only breaks direct `start`/`exec` with a
  cryptic vz error today, so v0.1 keeps "sealed by convention".
