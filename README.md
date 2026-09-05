# SpoolSmith

SpoolSmith finds network printers, works out the right OEM driver from a small family-oriented
catalog, shows you exactly what it's about to do, and — only after you say yes — installs the
driver and sets up the port. It's a CLI tool, not a service, not an agent, and not an RMM.

**Status: v0.1.0.** Detection is real. Installation is implemented but intentionally inert for
now — see [Current limitations](#current-limitations) below before trying it against a real
printer you care about.

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

Every command's `stdout` is plain JSON, so it's safe to pipe. Interactive prompts and
human-readable summaries go to `stderr`.

## Current limitations

Read this before pointing SpoolSmith at a printer you actually depend on:

- **Two families only:** HP LaserJet Pro M4xx and Brother HL-L2xxx. Anything else fails closed at
  the detection stage, not at install time.
- **Install doesn't run yet, on purpose.** Windows registers OEM drivers under a specific internal
  name, and nobody has confirmed those exact names against real `Get-PrinterDriver` output on real
  hardware. Rather than guess a plausible-looking name, `install` refuses to build a plan until
  that's verified. See [`docs/real-hardware-verification.md`](docs/real-hardware-verification.md)
  for the exact runbook that closes this out.
- **Windows only** for detection *and* install today. The codebase is structured so macOS/Linux
  support is a smaller lift later, not a rewrite — but it isn't built yet.
- No signature verification beyond what Windows itself already does on the driver package.

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
