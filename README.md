# SpoolSmith

SpoolSmith discovers network printers, saves reusable printer profiles, and maps Windows
queues using locally installed drivers after you review the plan. A small family catalog
also provides automatic identification and driver guidance.

**v0.2.0: Windows command-line app. A native GUI is planned next.** Live discovery and reusable JSON profiles
are implemented. Profile installation maps a queue using an already-installed Windows
driver, or stages the reviewed local Brother package if that driver is missing.
Automatic package downloads and broader package coverage are still pending.

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

Building from source needs Go 1.22+:

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
