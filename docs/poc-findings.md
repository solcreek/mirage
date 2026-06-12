# PoC findings: clone / suspend / resume timing

Measured 2026-06-11 by `poc/switchbench` (Go + `Code-Hex/vz/v3`).

**Host:** Apple Silicon, macOS 26.3.1, 32 GB RAM, internal SSD.
**Guest:** macOS 26.3.1, 4 vCPU, 4 GB RAM, 1920×1080 graphics device attached, NAT.
Bench: clone base → cold boot → 90 s settle → suspend; then 3 switch cycles of restore→resume→suspend across two VMs (A, B).

## Results (seconds)

| op | n | min | median | max |
|---|---|---|---|---|
| clone (disk+aux clonefile) | 2 | 0.01 | 0.01 | 0.01 |
| cold_boot_to_running (VZ state, not full OS boot) | 2 | 0.30 | 0.37 | 0.45 |
| suspend (save) | 8 | 1.88 | 2.39 | 2.74 |
| restore (load state) | 6 | 3.65 | 3.96 | 4.49 |
| resume_to_running | 6 | 0.19 | 0.20 | 0.22 |

**Save file size: 1.48 GB** for a 4 GB-RAM guest (VZ writes touched pages, not full RAM).
**Install time: 5m58s** for macOS 26.3.1 from IPSW.

## Perceived switch between two macOS VMs

- **Both kept resumed (within the 2-running-VM limit):** the switch is just `resume` ≈ **0.2 s — effectively instant.**
- **Suspend-rotate (more than 2 VMs, swap one out for another):** `suspend + restore + resume` ≈ **6.55 s** (median), dominated by `restore` (~4 s loading 1.48 GB of state).
- **Suspend-rotate with async save** (save the outgoing VM in the background after the new one is shown): perceived ≈ `restore + resume` ≈ **4.2 s**.

## Implications for the product

1. **macOS-guest save/restore through vz works on macOS 26** — 6 restore cycles, zero failures, graphics device attached. This was the unproven, Linux-only-evidence assumption (plan spike S5). It is now validated; suspend/resume can be promoted from "v0.2, gated" toward a confirmed capability.
2. **Density is better than feared:** ~1.5 GB per suspended VM on disk (not ~RAM), so many suspended macOS VMs fit on disk cheaply.
3. **"Switching between multiple macOS" is feasible:** instant (0.2 s) for the two that fit the concurrent-VM limit; 4–6 s suspend-rotate beyond that — comparable to waking a sleeping Mac.
4. **Clone is genuinely instant** (10 ms), so clone-from-golden as the default lifecycle holds.
5. **Install is ~6 min**, not the 15–25 min the plan budgeted — golden-image creation is cheaper than assumed.

## Caveats / not yet measured

- `cold_boot_to_running` is the VZ state transition, **not** time-to-login or time-to-agent-heartbeat. Full boot-to-usable needs the guest agent (separate spike S1/S2).
- Save/restore validated only for the **same instance on the same host** — never across clone identity rotation (an Apple constraint, not measured here).
- Single host, single run, idle guest. Numbers under memory pressure / many concurrent VMs are not yet measured.
- macOS 26.3.1 guest only; behavior on other guest majors not tested.
