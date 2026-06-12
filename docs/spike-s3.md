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

## Result

_pending first run_
