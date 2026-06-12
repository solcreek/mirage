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

### Known remaining wrinkle (macOS 26)

After seeding, the first capture still triggered a one-time consent dialog —
"mirage-agent is requesting to bypass the system private window picker and
directly access your screen" — and the capture *succeeded* (the dialog was
in the shot). macOS 15+/26 adds this periodic "direct screen access" consent on
top of the TCC grant (SCContentSharingPicker). For fully-unattended *recurring*
capture this needs suppressing too (likely an additional TCC/preference key);
tracked as follow-up. Single captures work today.

### Durability follow-up

The agent is ad-hoc signed, so its cdhash changes on every rebuild and the seed
must be re-run. A stable self-signed signing identity for the agent would make
the grant survive rebuilds (and is needed before sealing a real golden image).
