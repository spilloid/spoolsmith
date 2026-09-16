# SpoolSmith

SpoolSmith discovers network printers, saves reusable printer profiles, copies a working
printer setup from one PC to another, and maps Windows queues using locally installed
drivers after you review the plan. A small family catalog also provides automatic
identification and driver guidance.

**v0.5.0 is the published release: the command-line tool and the native Windows desktop app.**
This release adds the machine-to-machine copy workflow (`printers`, `copy`, `apply`,
`bundle inspect`), offline provisioning (`--offline`, `status`), and `repoint` for moving an
existing queue to a new address. Automatic package downloads and broader package coverage are
still pending.

The copy workflow and the offline workflow were verified against real Windows 11 hardware;
the desktop app does **not** yet expose copy or apply. See
[Current limitations](#current-limitations) for exactly what is and is not confirmed.

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

Build the desktop app. Its application manifest is embedded in the binary, so
the executable is self-contained and can be copied or renamed freely:

```powershell
go build -ldflags="-H windowsgui" -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui
```

The app opens on **Find a printer** and scans the network this PC is already on,
detected from its connected Wi-Fi or primary Ethernet adapter. Override the subnet
in **Network or IP** and choose **Scan**, or skip discovery entirely by entering a
known address under **Already know the printer's IP address?**.

Selecting a discovered printer and choosing **Set up selected printer** goes
straight to a preview when that address already has a saved setup — launch, scan,
review is the whole path. **Set up with different settings** ignores the saved
setup and opens **Add printer**, where you name the printer and pick its Windows
driver; the exact installed names come from **Refresh drivers**, and the model the
printer reported selects an unambiguous match for you to confirm. **Save and
review** captures the printer's evidence, writes the setup, and opens the preview.

**Saved printers** lists what this PC has stored, and set up, update or remove each
one. **Edit settings** changes a saved file in place, keeping a backup. Profile
paths and package archives stay portable: relative archive paths resolve beside the
profile.

### Desktop tests

`test/gui` drives the built executable with FlaUI. It needs a real desktop
session, so run it locally rather than in CI:

```powershell
go build -o dist/spoolsmith-gui.exe ./cmd/spoolsmith-gui
dotnet test test/gui/SpoolSmithGui.Tests
```

No test confirms an install or changes a Windows printer. Two intermittent
failures are known and unresolved: a launched app occasionally exits before the
tests can attach to it, and a control occasionally still reports itself
offscreen after its tab is selected. Both are test-harness timing, not app
behaviour — but they are why this suite does not gate CI yet. Re-run before
concluding a failure is real.

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

# Reverses exactly what install set up
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
- **Direct `install <ip>` still lacks verified built-in driver names.** Profiles supply
  the exact registered driver name without changing the built-in catalog.
- **Profiles map a driver to a RAW TCP 9100 queue.** The reviewed Brother local-archive
  recipe can stage a missing driver; other drivers must already be registered.
  IPP-only drivers/printers and LPR-only printers require different queue strategies;
  the current install plan uses the Windows standard TCP/IP port with its RAW default.
- **Discovery requires an explicit IPv4 CIDR** (`/24` through `/32`); it does not yet
  discover across VLANs or implement multicast discovery. Candidates are not certified printers.
- **The copy workflow is verified end to end on Windows 11 (build 26200).** Confirmed against
  real hardware: listing queues; `copy --include-driver` exporting a 115-file, 25.9 MB Brother
  package; bundle write, re-read and hash verification; `apply --dry-run` matching the live
  printer's identity to the capture; `apply` running idempotently; a reviewed plan fingerprint
  accepted and a wrong one refused. The driver-staging path was then proved directly: with the
  driver deregistered *and* its driver-store package deleted, `apply` verified the payload's
  catalog signature (Microsoft Windows Hardware Compatibility Publisher), staged it with
  `pnputil /add-driver` as `oem16.inf`, registered it, and created the queue.
  **Still unverified:** a live `repoint` mutation (only its preview was run), and no test print
  has been sent through a bundle-staged driver.
- **`uninstall --purge-driver` can retain a driver that is actually unused.** Windows removes a
  queue asynchronously, so the in-use check that guards driver removal can still see the queue
  that was just deleted and keep the driver. Observed on real hardware. Removing such a driver
  afterwards needs a spooler restart before Windows stops reporting it as in use. Not fixed in
  v0.5.0.
- **The desktop app cannot copy or apply.** `printers`, `copy`, `apply`, `bundle inspect` and
  `repoint` are command-line only in v0.5.0. The GUI keeps its existing find/set-up/review
  workflow. CLI/GUI parity for the copy workflow is the next release's work.
- **A copied bundle carries driver files from another machine's driver store.** That is a
  different provenance from the vendor-installer path: the bundle's hashes detect corruption and
  casual edits, and Windows' own driver-signing enforcement is what actually gates staging. Treat
  a bundle as trusted exactly as much as the machine it came from.
- **Live discovery, add and repeated add are verified with a Brother HL-L2315D.**
  Real Windows queue/port reads confirmed the mapping and repeat-add no-op behavior.
  The operator also observed a successful physical test print. Removal/configuration
  tests use PowerShell cmdlet doubles; live removal remains unverified.
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

## Offline provisioning and Intune packaging (in source)

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

The desktop **Tools → Build an Intune printer app** wizard and
`spoolsmith intune wizard` export reviewable Win32 app content. Automation uses
`spoolsmith intune build --help`. The packager requires a pinned compatible Windows
x64 CLI, a prevalidated profile, and a supported local archive or an explicit
separately managed driver prerequisite. It generates silent SYSTEM install/removal
scripts, local-only detection, protected persistent deployment state and logs,
and content-preparation instructions.

See the [complete Intune tutorial and lifecycle checklist](docs/intune-deployment.md)
and [illustrative example](examples/intune/README.md). Windows/SYSTEM, Company Portal,
and Intune pilot verification remain pending; automated script tests are not tenant
validation.

**The Intune commands are not in the v0.5.0 release.** `intune wizard`, `intune build` and
`capabilities` are present in source and covered by tests, but are deliberately off the shipped
command table until the packaging has been piloted against a real tenant. The offline
provisioning commands above (`--offline`, `status`) *are* released.
