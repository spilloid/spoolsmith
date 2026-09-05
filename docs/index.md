---
layout: default
title: SpoolSmith
---

# SpoolSmith

Network printer detection, OEM driver resolution, and a driver install you actually get to review
before it happens.

**Current version: v0.1.0.** Detection is real and live. Installation is fully built but
intentionally refuses to run yet — see [Current limitations](#current-limitations).

[Download the latest Windows build →](https://github.com/spilloid/spoolsmith/releases/latest){: .btn}
[View source on GitHub →](https://github.com/spilloid/spoolsmith){: .btn}

## The problem this solves

Setting up a network printer is a small, recurring, mildly annoying IT chore: find the device on
the network, figure out what it actually is, track down the right driver, install it, point it at
the right port. SpoolSmith automates the finding and figuring-out, and turns the install itself
into one reviewed decision instead of a wizard full of dropdowns.

## Design principles, not just a feature list

**Detection is automatic. Installation never is.** SpoolSmith fingerprints and resolves a printer
on its own — you don't hand-enter fields for every device. But nothing gets written to Windows'
driver store or print spooler without SpoolSmith showing you the *exact* plan — the driver, the
port, the literal commands — and getting one explicit yes first. There's no flag that skips this,
including the ones meant for scripting.

**No fuzzy installs.** Ambiguous or conflicting evidence means SpoolSmith stops and tells you why,
rather than guessing its way to a plausible-looking answer.

**A small catalog, deliberately, not a database.** Printer identity resolves through a family
hierarchy —

```
observed identifiers → normalized model → printer family → driver package/strategy
```

— so supporting a new printer means adding one family, not one more row to an ever-growing table
of individual models.

**No network fetch of driver packages.** SpoolSmith only uses a driver that's already available —
via Windows Update or a vendor package you staged yourself. It does not download and run an
installer from the internet on your behalf. That's a real design choice, not a missing feature:
fetch-and-execute is exactly the kind of thing this project is built to avoid.

**No credentials, no RMM behavior.** SpoolSmith reads what a printer volunteers over SNMP, HTTP,
and PJL — nothing it wasn't asked to share. It never asks for a password, never runs an arbitrary
command, and never does anything unattended or on a schedule.

## Try it

```sh
# Live detection against a real device
spoolsmith inspect 192.168.1.50

# What a device actually returns, useful for adding a new printer family
spoolsmith catalog probe 192.168.1.50

# What SpoolSmith currently recognizes
spoolsmith catalog families

# Shows a plan, asks you to confirm, only then touches anything
spoolsmith install 192.168.1.50
spoolsmith install 192.168.1.50 --dry-run

# Reverses exactly what install set up
spoolsmith uninstall "HP LaserJet Pro M404dn"
```

Every command's `stdout` is plain JSON — safe to pipe into a script. Prompts and human-readable
summaries go to `stderr`, so the two never get tangled together.

## Current limitations

Worth reading before pointing this at a printer you depend on:

- **Two printer families today:** HP LaserJet Pro M4xx and Brother HL-L2xxx. Anything else fails
  closed at detection, before install is even considered.
- **Install is built but deliberately inert.** Windows registers an OEM driver under a specific
  internal name, and that exact name hasn't been confirmed yet against real `Get-PrinterDriver`
  output on real hardware for either family. Rather than ship a plausible-looking guess,
  `install` refuses to build a plan until that's verified on a real machine. This is the actual
  next milestone.
- **Windows only**, for both detection and install, today. The codebase already separates
  platform-specific mutation behind a small interface — extending to macOS/Linux is a real future
  item, not a rewrite, but it isn't built yet.

## How it's actually built

- `internal/probe` — live SNMP/HTTP/PJL/port/OUI/hostname fingerprinting. Zero third-party Go
  dependencies.
- `internal/catalog` — family/driver resolution. Pure functions, fails closed on ambiguity.
- `internal/inspect` — assembles the reviewable result shown for `inspect`.
- `internal/install` — the only Windows-specific, mutating code in the whole project, kept behind
  a narrow interface so everything else stays testable without a Windows machine.

Every one of those went through an independent adversarial review before being called done — real
findings, including a genuine command-injection path caught before it shipped. The full, honest
log — defects found, what was real, what got fixed, what's still open — lives in
[`docs/dev-process.md`](dev-process.md). It's the actual engineering record, not cleaned-up
marketing copy.

## License

MIT — see [LICENSE](https://github.com/spilloid/spoolsmith/blob/main/LICENSE).
