# SpoolSmith

SpoolSmith discovers network printers, saves reusable printer profiles, copies a working
printer setup from one PC to another, and maps Windows queues using locally installed
drivers after you review the plan. A small family catalog also provides automatic
identification and driver guidance.

**v0.6.0 includes the command-line tool and the native Windows desktop app.**
The desktop now mirrors the copy, apply, installed-printer inventory, address-change,
offline setup and local-status workflows. Saved setups can be exported and imported
in bulk as JSON, and the app has a lighter layout with blue accents.

The underlying copy and offline workflows have real Windows 11 validation from
v0.5.0. See [Current limitations](#current-limitations) for the validation boundaries.
Automatic downloads and Intune packaging remain outside the shipped surface.

## Copy a printer from one PC to another

The case this exists for: someone needs a printer, someone else already has it working, and
you would rather not rediscover the driver name, the address, and the port settings by hand.

On the PC that already prints:

```powershell
# What does this PC have? (read-only; wraps Get-Printer and Get-PrinterPort)
spoolsmith printers

# Copy one of them. Omit the queue name to pick from a numbered list;
# omit the file name to have it named after the queue.
spoolsmith copy "Accounting" accounting.ssb --include-driver
```

`--include-driver` exports the driver package out of the Windows driver store so the target PC
does not need it beforehand. It requires an elevated prompt, and SpoolSmith checks that before
doing any other work. Without it the bundle carries the mapping only, and the target PC must
already have that driver registered.

Copy `accounting.ssb` to the other PC however you normally move a file, then:

```powershell
# Read the bundle without touching the network or this PC
spoolsmith bundle inspect accounting.ssb

# Preview against the real printer, then apply after one confirmation
spoolsmith apply accounting.ssb --dry-run
spoolsmith apply accounting.ssb
```

`apply` re-checks the printer's live identity against what was captured at copy time, so it
fails closed if the address now answers as a different device. When the printer is not
reachable — a client site you are preparing for, a machine on another VLAN — `apply --offline`
skips that check and says so in the plan before you confirm it.

A bundle is a plain zip: a manifest carrying configuration and provenance, and optionally the
exported driver files, each listed with its size and SHA-256 and verified on extraction. It
contains no commands. The hashes are tamper-evidence, not a signature — the trust anchor for a
driver payload is Windows' own catalog signature check when `pnputil` stages the INF.

### Rolling the same reviewed setup out to several PCs

`--dry-run` prints a fingerprint of the exact plan you reviewed. Passing it back with
`--plan-hash` accepts that one plan and refuses every other, so a machine that would have
computed something different stops instead of mutating:

```powershell
spoolsmith apply accounting.ssb --dry-run          # prints: Reviewed plan fingerprint: 5364...
spoolsmith apply accounting.ssb --plan-hash 5364...
```

This is narrower than `--yes`, not broader: `--yes` accepts whatever plan the machine computes,
sight unseen.

### Queues that cannot be copied

`printers` marks these with `!` and states why. SpoolSmith only reproduces RAW TCP 9100 queues
pointed at a literal IP address, because anything else would quietly build a different queue on
the next PC. An LPR queue, a non-9100 port, a port naming a host rather than an address, and
"Microsoft Print to PDF" are all refused rather than approximated.

## Native Windows GUI

Download and extract the [Windows release ZIP](https://github.com/spilloid/spoolsmith/releases/latest),
then open `spoolsmith-gui.exe`. No Go installation is needed. Its application
manifest is embedded, so the executable can be copied or renamed freely.

For developers building the desktop from source:

```powershell
go build -ldflags="-H windowsgui" -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui
```

The app opens on **This PC**, showing Windows' installed printers. Select one to
**Copy to a file**, **Change address**, or **Remove printer**. Copying can include the
driver; exporting driver files requires administrator rights.

**Add a printer** combines network discovery and printer settings. Enter a subnet
and **Scan**, or enter one address and choose **Use IP directly**. Choose a compatible
installed driver and **Save and review**, or use **Review catalog setup** for catalog
resolution. **Open a printer file** reads a copied `.ssb`; **More options** on its
review offers offline setup and updating an existing queue. **Tools → Inspect** can
also verify a bundle and show its complete manifest without contacting the printer.
**Tools → Build an Intune printer app...** packages a reviewed profile into a
local, reviewable Win32 app bundle — see [Intune packaging](#intune-packaging).

**Open a saved setup** lists reusable profiles. Set up, update, remove, edit with a
backup, or **Check status** against local Windows configuration. Status does not
prove reachability or printing. **Open another folder** switches the profile library.
The default library sits beside the executable; `SPOOLSMITH_PROFILES_DIR` can override it.

**Export all JSON** saves every profile in the current folder into one versioned
collection. **Import all JSON** validates the entire collection, preserves all profile
properties and evidence, and refuses existing filenames (including case-only clashes).
Import saves files only; each Windows change still needs review and confirmation.
Driver archives are not embedded: carry them separately and preserve their relative
paths beside the imported profiles. Keep collection exports outside the profile folder.

The CLI exposes the same transfer:

```powershell
spoolsmith profile export-all profiles printer-setups.json
spoolsmith profile import-all printer-setups.json imported-profiles
```

### Desktop tests

`test/gui` drives the built executable with FlaUI. It needs a desktop session;
run it locally or through the manual **Desktop validation** workflow:

```powershell
go build -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui
go build -o dist/spoolsmith.exe ./cmd/spoolsmith
dotnet test test/gui/SpoolSmithGui.Tests
```

No test confirms an install or changes a Windows printer. The latest
[hosted Windows run](https://github.com/spilloid/spoolsmith/actions/runs/35181393861)
passed 28/28 checks. Earlier runs exposed launch/attachment timing and offscreen
control lookups; investigate any recurrence using the captured screenshots and
TRX evidence. Regular CI compiles this suite; desktop execution is a separate
manual workflow rather than a required CI gate.

`SPOOLSMITH_CAPTURE_SITE_SHOTS=1` additionally regenerates the product site's
screenshots from the running app.

**Preview changes** shows the proposed changes; **Full plan / JSON** adds commands
and preflight details, and **Scan details** holds the raw discovery output.
Execution requires confirmation, and changing any input invalidates the preview.
The shared workflow also rejects a changed plan during execution. Run as
administrator when applying queue changes. The CLI provides the same operations;
`spoolsmith drivers` lists registered driver names.

## Daily printer mapping

Run discovery on the subnet you are working on (at most a `/24`). If you already
know the IP, skip discovery:

```powershell
.\spoolsmith.exe discover 192.168.1.0/24
.\spoolsmith.exe inspect 192.168.1.50
Get-PrinterDriver | Select-Object Name, Manufacturer
```

Install the appropriate OEM driver locally first if it is absent. Copy its exact
registered `Name` into the capture command. Choose the driver based on verified
compatibility; an LLM-generated name or a catalog family is not that verification.

```powershell
New-Item -ItemType Directory -Force profiles
.\spoolsmith.exe profile capture 192.168.1.50 profiles\office.json --name "Office Printer" --driver "EXACT REGISTERED OEM DRIVER NAME"

# In an Administrator PowerShell, preview and then confirm the mapping:
.\spoolsmith.exe add --profile profiles\office.json --dry-run
.\spoolsmith.exe add --profile profiles\office.json

# Change the saved settings, then review/apply them to the named queue:
.\spoolsmith.exe profile edit profiles\office.json --driver "NEW REGISTERED DRIVER NAME"
.\spoolsmith.exe configure --profile profiles\office.json --dry-run
.\spoolsmith.exe configure --profile profiles\office.json

# Remove the queue, retaining shared ports and drivers:
.\spoolsmith.exe remove --profile profiles\office.json
```

Keep one JSON per printer and copy it to the workstation where you need the queue.
The `profiles/` directory is ignored by Git. Capture never overwrites a file.
Profiles support printers outside the built-in family catalog through an explicit
operator-selected driver. They contain configuration and observed evidence, never
shell commands. Installation re-probes the target and requires the saved HTTP/PJL
identity to agree before showing the plan and requesting confirmation. One missing
model source is tolerated if another saved model source agrees; conflicting sources
still stop the operation. Unavailable identity is retried once. SNMP-only captures
are supported, but SNMP alone cannot replace saved HTTP/PJL model evidence.
This checks observed model continuity, not a unique device identity or authentication.
Firmware changes or missing evidence can require a new capture.

Repeating `add` with matching settings leaves the queue and port unchanged.
Different queue settings require `configure`; conflicting port endpoints always stop.
Repeating `remove` on an absent queue succeeds without changes. Shared resources are
retained, and drivers are retained by default. Profile edits preserve a backup;
changing the queue name creates a separate queue rather than renaming the old one.
Moving a queue to another IP retains its previous port.
`remove --profile` checks the installed endpoint and driver against the profile;
use explicit removal by queue name if you intend to remove a differently configured queue.
Edit backups live under `.backups/` with a `.bak` extension, outside normal JSON globs.

Terminal add/configure/remove commands show concise plans and results. Use
`--dry-run --json` to inspect the complete commands and metadata. Redirected output
remains JSON for scripting. `install`/`uninstall` remain supported aliases.

The version-1 profile fields are `version`, `target` (IP address), `printer_name`,
`driver_name`, and `evidence`. You can edit the queue/driver names; validate a change
with `install --profile ... --dry-run`. If the printer moves, update `target`;
retain the original capture in `evidence`. A driver database can later share
validated package definitions across these per-printer records.

## Optional local Brother driver package

For the verified Brother HL-L2315D driver on Windows x64, a profile can reference
the reviewed package recipe and a locally downloaded archive. Keep the archive next
to your profiles; relative paths resolve from the profile directory, not your shell.

```powershell
.\spoolsmith.exe profile edit profiles\brother-home.json --package brother-y14a-c1-hostm-1110 --archive .packages\brother\Y14A_C1-hostm-1110.EXE
.\spoolsmith.exe add --profile profiles\brother-home.json --dry-run
.\spoolsmith.exe add --profile profiles\brother-home.json

# Return to using an already-installed driver only:
.\spoolsmith.exe profile edit profiles\brother-home.json --clear-package
```

The optional `driver_package` object contains `id` and `archive`. The shown plan
includes its source URL, pinned SHA-256, and staging action. One confirmation covers
driver setup and queue mapping. A registered driver is reused without opening the
archive. Otherwise SpoolSmith checks the hash and vendor signature, extracts the
archive without running its EXE, verifies the driver catalog signature, stages the
INF with Windows, and registers the exact model driver. The archive is held read-only
through verification and extraction. Staging directories are retained in Windows
temp for diagnostics; a later failure can leave a staged driver or unused port.
Dry-run does not read/verify the archive or stage anything; it previews those actions.
Changing the printer's driver requires a matching package recipe or clearing it.

## Why

Setting up a network printer on Windows is still a small, recurring chore for anyone doing IT
support: find the thing, figure out what it actually is, find the right driver, install it,
point it at the right port. SpoolSmith automates the first three steps and makes the last two a
single reviewed decision instead of a wizard.

## How it's built to behave

- **Detection is automatic. Installation is not.** SpoolSmith fingerprints and resolves a printer
  on its own — no manual prompts for every field. But nothing ever gets written to Windows' driver
  store or print spooler without showing you the exact plan first and getting one explicit yes.
  There is no flag that skips this.
- **No fuzzy installs.** If the evidence is ambiguous or conflicting, SpoolSmith says so and stops
  — it never guesses its way to an install.
- **Small catalog, not a database.** Printer identity resolves through a family hierarchy
  (`observed identifiers → normalized model → printer family → driver package`), not a
  hand-maintained table of every model ever made.
- **No network fetch of driver packages.** SpoolSmith only uses drivers already present via
  Windows Update or a vendor package you staged yourself — it never downloads and runs an
  installer from the internet on your behalf.
- **No credentials, no RMM behavior.** SpoolSmith reads what a device tells you over SNMP/HTTP/PJL
  and that's it. It doesn't ask for passwords, doesn't run arbitrary commands, and doesn't do
  anything unattended or scheduled.

## Install

Grab the latest Windows build from [Releases](https://github.com/spilloid/spoolsmith/releases).
It's a single `spoolsmith.exe` — no installer, no dependencies.

Building from source needs Go 1.24+:

```sh
git clone https://github.com/spilloid/spoolsmith.git
cd spoolsmith
go build ./cmd/spoolsmith
```

## Usage

```sh
# Point it at a fixture file (for testing) or a real IP (live detection)
spoolsmith inspect 192.168.1.50
spoolsmith inspect fixtures/hp-laserjet-m404-synthetic.json

# See the raw evidence a device returns, useful when adding catalog support for a new model
spoolsmith catalog probe 192.168.1.50

# List the printer families SpoolSmith currently recognizes
spoolsmith catalog families

# Install — shows a plan, asks for confirmation, then (and only then) mutates anything
spoolsmith install 192.168.1.50
spoolsmith install 192.168.1.50 --dry-run     # see the plan, touch nothing
spoolsmith install 192.168.1.50 --force-family hp-laserjet-m4xx

# Remove the named queue; retain ports and drivers still used by other queues
spoolsmith uninstall "HP LaserJet Pro M404dn"

# What this PC already has, and which queues can be copied elsewhere
spoolsmith printers
spoolsmith printers --copyable --json

# Move an existing queue to a new address, keeping its name and driver
spoolsmith repoint "Accounting" 192.168.1.75 --dry-run
spoolsmith repoint "Accounting" 192.168.1.75
```

Data-command and redirected `stdout` is JSON. Interactive prompts and human-readable
summaries go to `stderr`. Terminal add/configure/remove uses human output by default;
pass `--json` to request the machine-readable outcome explicitly.

## Current limitations

Read this before pointing SpoolSmith at a printer you actually depend on:

- **Automatic catalog resolution covers two families:** HP LaserJet Pro M4xx and Brother
  HL-L2xxx. Other printer candidates remain visible in discovery; use an explicitly
  configured profile to map them.
- **Automatic driver naming is verified only for the Brother HL-L2315D.** Other models
  need a profile with an explicitly selected, compatible Windows driver.
- **Profiles map a driver to a RAW TCP 9100 queue.** The reviewed Brother local-archive
  recipe and copied bundles with driver payloads can stage a missing driver. Otherwise,
  the compatible driver must already be registered.
  IPP-only drivers/printers and LPR-only printers require different queue strategies;
  the current install plan uses the Windows standard TCP/IP port with its RAW default.
- **CLI discovery requires an explicit IPv4 CIDR** (`/24` through `/32`). The desktop
  can derive the local subnet or accept a single IP. Discovery does not automatically
  cross VLANs or implement multicast discovery. Candidates are not certified printers.
- **Copy and apply were tested on one Windows 11 PC (build 26200).** Confirmed against
  real hardware: listing queues; `copy --include-driver` exporting a 115-file, 25.9 MB Brother
  package; bundle write, re-read and hash verification; `apply --dry-run` matching the live
  printer's identity to the capture; `apply` running idempotently; a reviewed plan fingerprint
  accepted and a wrong one refused. The driver-staging path was then proved directly: with the
  driver deregistered *and* its driver-store package deleted, `apply` verified the payload's
  catalog signature (Microsoft Windows Hardware Compatibility Publisher), staged it with
  `pnputil /add-driver` as `oem16.inf`, registered it, and created the queue.
  **Still unverified:** transfer between two separate PCs, a live `repoint` mutation
  (only its preview was run), and physical printing through a bundle-staged driver.
  The absent-driver target above was simulated on the same PC, not a second machine.
  See the [copy validation record](docs/validation/2026-09-15-copy-workflow.md).
- **`uninstall --purge-driver` can retain a driver that is actually unused.** Windows removes a
  queue asynchronously, so the in-use check that guards driver removal can still see the queue
  that was just deleted and keep the driver. Observed on real hardware. Removing such a driver
  afterwards needs a spooler restart before Windows stops reporting it as in use. Still present in
  v0.6.0.
- **A copied bundle carries driver files from another machine's driver store.** That is a
  different provenance from the vendor-installer path: the bundle's hashes detect corruption and
  casual edits, and Windows' own driver-signing enforcement is what actually gates staging. Treat
  a bundle as trusted exactly as much as the machine it came from.
- **Live discovery, add and repeated add are verified with a Brother HL-L2315D.**
  Real Windows queue/port reads confirmed the mapping and repeat-add no-op behavior.
  The operator also observed a successful physical test print. Native Windows offline
  add/configure, removal and shared-resource protection have also been exercised;
  see the [Windows pilot results](docs/validation/2026-09-15-windows11-results.md).
  Automated tests supplement that record; they do not prove additional hardware support.
- Matching mappings are reused. Conflicts fail closed, and shared ports/drivers are
  retained on removal. This is repeatable reconciliation, not a transaction: a process
  failure can leave an unused port, which a subsequent add will safely reuse.
- Printer inventory errors stop the operation rather than being interpreted as
  absence; a broken Windows print provider can therefore block mapping.
- **Windows only** for detection *and* install today. The codebase is structured so macOS/Linux
  support is a smaller lift later, not a rewrite — but it isn't built yet.
- The local package recipe checks the pinned archive hash and Windows Authenticode
  signatures; Windows validates driver-store staging.

## How it's put together

```
observed identifiers  →  normalized model  →  printer family  →  driver package/strategy
```

- `internal/probe` — live SNMP/HTTP/PJL/port/OUI/hostname fingerprinting. Zero third-party
  dependencies.
- `internal/catalog` — the family/driver resolution logic. Pure functions, fails closed on
  ambiguity, no I/O.
- `internal/inspect` — assembles the reviewable `inspect` result.
- `internal/install` — Windows-only driver/port mutation, kept behind a small interface so the
  rest of the codebase stays testable without a Windows machine.

Every one of these has been through an independent adversarial review pass before being called
done — see [`docs/dev-process.md`](docs/dev-process.md) for the actual log, defects found, and
what got fixed. It's not polished marketing copy; it's the real record, warts included.

## License

MIT — see [LICENSE](LICENSE).

## Offline provisioning (released)

Prevalidated profiles can provision a queue before the printer is reachable:

```powershell
spoolsmith add --profile .\profiles\accounting.json --offline --dry-run --json
spoolsmith add --profile .\profiles\accounting.json --offline --yes --json
spoolsmith configure --profile .\profiles\accounting.json --offline --yes --json
spoolsmith status --profile .\profiles\accounting.json --json
```

`--offline` explicitly skips live identity checks and requires a valid saved
profile; positional targets and forced-family overrides are rejected. It never
falls back from a failed live probe and never downloads drivers. Existing
confirmation, elevation, supported local-package verification and conflict checks
remain in force. Offline success additionally verifies local queue/driver/RAW TCP
9100 configuration. Neither success nor `status` proves reachability or printing.
`status` is local-only: exit 0 means matching configuration, 3 means mismatch,
2 means invalid inputs, and 1 means inventory/execution failure.

## Intune packaging

`spoolsmith intune` exports a reviewable Win32 app package for a validated profile:
silent SYSTEM install/uninstall/detect scripts, a protected local deployment
record, and a README with the exact install command, uninstall command and
detection rule to paste into Intune. It never signs in to a tenant, uploads
anything, creates a group, or assigns an app — those steps stay manual, in the
Intune admin center, the same "no credentials, no unattended behavior" line
SpoolSmith draws everywhere else (see [How it's built to
behave](#how-its-built-to-behave)).

```powershell
spoolsmith intune build --profile printer-setups\accounting.json `
  --binary spoolsmith.exe --driver-prerequisite --dry-run
```

The profile supplies the app name, description and suggested deployment ID;
revision defaults to 1. The CLI hash is calculated automatically. An unused
export folder beside the profile is suggested, so no folder name needs to be
invented. Override these with `--name`, `--description`, `--id`, `--revision`,
`--binary-sha256` or `--output`. For updates, keep the existing deployment ID
and increase its revision. Location is optional. Driver prerequisites, offline
provisioning and adoption still require explicit choices.

`--dry-run` previews the manifest and destination without writing files. After
review, repeat without it to export; supply the reviewed `--binary-sha256` and
`--output` when you need to fix those across separate invocations. For an
interactive review and separate export confirmation, use `intune wizard` or
the desktop GUI’s Tools tab → **Build an Intune printer app...**. The GUI has two
pages: settings and review/export, with optional metadata and policy under
**Advanced settings**. Both wizards retain the reviewed payload pins until export.

`--content-prep-tool`/`--content-prep-output` additionally run Microsoft’s own
`IntuneWinAppUtil.exe` locally to produce the `.intunewin` file; without them,
`README.txt` in the export names the exact command to run it yourself.
The CLI must be a Windows x64 SpoolSmith build with the `intune-endpoint-v1`
capability marker (`spoolsmith capabilities`); older, unrelated and GUI binaries
are refused. Payload hashes are pinned and rechecked at export.

[Native Windows/SYSTEM tests](docs/validation/2026-09-15-windows11-results.md)
validated install, local detection, protected state, standard-user denials,
revision updates and removal end to end. The desktop wizard's simplified
two-page flow is validated on real Windows hardware in
[the UX-simplification record](docs/validation/2026-09-18-gui-intune-ux-simplification.md),
building on [the original dialog's validation](docs/validation/2026-09-17-gui-intune-wizard.md).
The [design and validation guide](docs/intune-deployment.md)
and [illustrative example](examples/intune/README.md) cover the full workflow
and lifecycle rules. See the [roadmap](docs/roadmap.md) for what's still open.
