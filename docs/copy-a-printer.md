# Copying a printer from one PC to another

Someone needs a printer. Someone else, two desks over, already has it working. This is the
shortest safe path between those two facts.

Everything here is v0.5.0 command-line. The desktop app does not do this yet.

## The short version

On the PC that already prints, in an elevated PowerShell:

```powershell
spoolsmith printers
spoolsmith copy "Accounting" accounting.ssb --include-driver
```

On the PC that needs the printer:

```powershell
spoolsmith apply accounting.ssb --dry-run
spoolsmith apply accounting.ssb
```

Move `accounting.ssb` between them however you already move files. It is a plain zip and
nothing in it executes.

## Step 1 — see what the source PC has

```powershell
spoolsmith printers
```

```
Printers installed on this PC (2)
   1. SpoolSmith VM Brother   192.168.68.108 RAW/9100  [Brother HL-L2315D series]
!  2. Microsoft Print to PDF  PORTPROMPT:  [Microsoft Print To PDF]

1 queue(s) marked ! cannot be copied to another PC:
  Microsoft Print to PDF: its port "PORTPROMPT:" reports no printer host address; this is not
  a standard TCP/IP port SpoolSmith can reproduce
```

This reads Windows' own `Get-Printer` and `Get-PrinterPort` and changes nothing. It needs no
elevation.

A queue marked `!` is one SpoolSmith will not reproduce, and the reason is always stated. The
rule is narrow on purpose: only a RAW TCP 9100 queue pointed at a literal IP address can be
rebuilt elsewhere and be confident it is the same queue. LPR queues, non-9100 ports, ports
naming a host instead of an address, and virtual printers are refused rather than approximated.

`--json` gives the same listing machine-readably; `--copyable` drops the ones you cannot use.

## Step 2 — copy it

```powershell
spoolsmith copy "Accounting" accounting.ssb --include-driver
```

Both arguments are optional:

- Omit the queue name and you get a numbered list to choose from.
- Omit the file name and the bundle is named after the queue.

```powershell
spoolsmith copy            # pick from a list, write Accounting.ssb
```

Without a terminal — a scheduled task, an RMM, an Intune script — `copy` with no queue name
fails and tells you to name one. It does not choose on your behalf.

### What `--include-driver` does, and when you need it

It exports the driver package out of the source PC's Windows driver store with `pnputil`, and
carries it inside the bundle. Use it whenever the target PC might not already have that exact
driver registered.

It needs an elevated prompt. SpoolSmith checks for Administrator *before* reading the queue or
probing the printer, so you find out immediately rather than after a wait.

Without it, the bundle carries the mapping only, and `apply` fails on a target PC that lacks
the driver — telling you so rather than installing something else.

Some drivers cannot be exported at all. An inbox or Windows Update driver has no driver-store
package to copy, and SpoolSmith says exactly that: the target machine will need to obtain it the
same way this one did.

### What gets captured

SpoolSmith probes the printer itself at copy time and stores its identity — HTTP title, PJL
identity, SNMP description — in the bundle. This is what lets `apply` confirm on the other PC
that the address still answers as the same device.

If the printer is asleep, the first probe can come back with nothing useful. SpoolSmith retries
once before giving up, because a printer that answers thinly on first contact and fully a few
seconds later is normal. A second empty answer is a real failure and stops the copy.

## Step 3 — look at the bundle (optional)

```powershell
spoolsmith bundle inspect accounting.ssb
```

```
Bundle: accounting.ssb
  Created: 2026-09-16T02:52:59Z by spoolsmith v0.5.0
  Source machine: DESKTOP-BBQQE4J
  Queue: Accounting
  Target: 192.168.68.108 (RAW TCP 9100)
  Driver: Brother HL-L2315D series
  Driver payload: 115 files, 25906495 bytes, INF brohl13a.inf (from oem16.inf)
  All payload files match the manifest's hashes.
```

This touches neither the network nor the PC. It checks the bundle's integrity, not the printer.

## Step 4 — apply it on the other PC

```powershell
spoolsmith apply accounting.ssb --dry-run
```

The preview shows the queue, the target, the port, the driver, and — if a payload is carried —
how many files would be staged and only if the driver is missing. Nothing has changed yet.

Then, from an elevated prompt:

```powershell
spoolsmith apply accounting.ssb
```

One confirmation of the shown plan, and then it runs. Applying the same bundle twice is safe:
matching settings are reported unchanged rather than rebuilt.

### When the printer is not reachable

```powershell
spoolsmith apply accounting.ssb --offline
```

`--offline` skips the live identity check — the one that confirms the address answers as the
device that was captured. The plan says so before you confirm it:

```
  OFFLINE: live identity is not checked. Verify local queue, driver and RAW TCP 9100 endpoint
  after applying; printing requires network connectivity.
```

Use it when you are preparing a machine away from the printer. It does not prove the printer is
there, and it does not prove printing works.

## Rolling one reviewed setup out to several PCs

Review the plan once, by hand, and take the fingerprint it prints:

```powershell
spoolsmith apply accounting.ssb --dry-run
# ... Reviewed plan fingerprint: 536445b1f7c20f84...
```

Then name that exact plan on the remaining machines:

```powershell
spoolsmith apply accounting.ssb --plan-hash 536445b1f7c20f84...
```

Any machine whose computed plan differs in any respect stops instead of mutating, and tells you
both fingerprints. This is deliberately narrower than `--yes`: `--yes` accepts whatever plan the
machine computes without showing anyone; `--plan-hash` accepts one plan and refuses all others.

## Moving a printer that changed address

When the printer moved or the subnet was renumbered, the queue is fine and only its endpoint is
wrong. Rebuilding it would change the thing users pick from, to fix the thing they never see.

```powershell
spoolsmith repoint "Accounting" 192.168.1.75 --dry-run
spoolsmith repoint "Accounting" 192.168.1.75
```

The queue name and driver are kept. The old port is deliberately left in place, because another
queue may still print through it.

## What a bundle is, exactly

A plain zip, chosen because it has to open on a machine nobody prepared and Windows reads zip
natively:

- `manifest.json` — the queue, driver name, target address, captured evidence, and provenance.
  Configuration only. It contains no commands.
- `payload/` — the exported driver files, present only with `--include-driver`. Every file is
  listed with its size and SHA-256, and each is verified on extraction. Entries that are
  absolute, traversing, drive-relative, or duplicated are rejected before anything is written.

The hashes are **tamper-evidence, not authentication**. They detect corruption and casual edits.
They are not a signature. The real trust anchor for a driver payload is Windows' own catalog
signature check when `pnputil` stages the INF — the same anchor as the vendor-archive path.

A bundle from a colleague's PC deserves exactly as much trust as that PC.

## What has actually been verified

On Windows 11 build 26200, against a real Brother HL-L2315D:

- `printers` listing, and its copy-eligibility reasons.
- `copy --include-driver` exporting a 115-file, 25.9 MB package out of the driver store.
- The bundle being written, re-read, and every payload file hash-verified.
- `apply --dry-run` probing the live printer and matching its identity to the capture.
- `apply` running idempotently against an already-correct machine.
- A reviewed plan fingerprint accepted, and a wrong fingerprint refused.
- **The driver-staging path**, proved directly: with the driver deregistered and its
  driver-store package deleted, `apply` checked the payload catalog's signature (Microsoft
  Windows Hardware Compatibility Publisher), staged it with `pnputil /add-driver` as
  `oem16.inf`, registered it, and created the queue.

Still unverified: a live `repoint` mutation (only its preview has been run), and no test page
has been printed through a bundle-staged driver.

## Known gaps in v0.5.0

- The desktop app cannot copy or apply. Command line only for now.
- `uninstall --purge-driver` can leave a driver registered that nothing uses any more. Windows
  removes queues asynchronously, so the check guarding driver removal can still see the queue
  that was just deleted. If you then want the driver genuinely gone, restart the spooler first:
  until you do, Windows reports it as in use and both `Remove-PrinterDriver` and
  `pnputil /delete-driver` refuse.
