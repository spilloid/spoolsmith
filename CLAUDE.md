# CLAUDE.md

## Project Mission

**Operator update, 2026-09-06:** The operator asked Astra to prioritize daily use:
known-IP mapping, discovery, and reusable per-printer JSON. See
`docs/daily-use-spec.md`. Explicit operator profiles now extend queue mapping beyond
the two built-in catalog families, using an already-registered Windows driver and
fresh evidence checks. The historical two-family restrictions below still describe
automatic catalog selection and driver-package work; they do not prohibit this
operator-authorized profile mapping. The reviewed Brother local-archive recipe now
supports staging after the same plan confirmation. Downloads remain future work.

SpoolSmith identifies network printers, resolves the correct OEM driver through a small,
family-oriented catalog, presents a reviewable install plan, and installs/configures the printer
locally only after explicit user approval. It is a standalone product with its own CLI, its own
driver catalog, and its own OS-mutation logic — not a NetViz feature, not an RMM.

Governance: authorized by `corporate-strategy` board decision DR-0005
(`board/meetings/2026-09-03-spoolsmith-new-product/04-decision-record.md`,
`decisions/DECISION_LOG.md` D-0039) as a **milestone-one authorization, not a product
authorization**. See `corporate-strategy/state/products/SpoolSmith.md` for current status,
budget, and revisit triggers before assuming any scope beyond what's written here.

## Current scope: v0.1.0 — live detection + approved install for two named families

Milestone one (fixture-only detection) is done. Per corporate-strategy D-0040
(`decisions/DECISION_LOG.md`), OS mutation is now authorized, but **narrowly**:

1. Collect real fingerprint evidence live from the network (SNMP/HTTP/PJL/ports/OUI/hostname), or
   from a fixture file for testing.
2. Normalize evidence, resolve a printer family, then a driver package/strategy — unchanged,
   already fail-closed, already reviewed.
3. Emit a deterministic `inspect`/install-plan result — unchanged.
4. **For exactly two named families (HP LaserJet Pro M4xx; Brother HL-L2xxx), and only via the
   documented trust model in D-0040, install the driver and configure the TCP/IP printer port —
   after showing the full plan and receiving one explicit confirmation.** Detection and resolution
   run automatically; installation does not skip the confirmation step under any flag.

**Still no remote execution beyond SpoolSmith's own local fingerprinting/install commands, no
credential collection, and no network auto-fetch of driver packages** (D-0040's trust model is
explicit that installers must already be staged locally or resolved through Windows' own
`Add-PrinterDriver`/`pnputil` path — SpoolSmith does not itself download an installer from a URL).

Catalog hierarchy, deliberately layered so a giant hand-maintained per-model database is never the
shape of this system:

```
observed identifiers -> normalized model -> printer family -> driver package/strategy
```

Many model aliases should map to relatively few driver families. Driver package metadata (URLs,
versions, hashes, signatures — volatile OEM information) must stay a separate, swappable layer
from the family catalog (stable device-identity mappings) — see Architecture Rules below.

Definition of done for milestone one (DR-0005 §6, non-negotiable):
- The fixture set states explicitly which fingerprint evidence it assumes vs. what's actually
  obtainable from real hardware.
- At least one fixture per some claimed family is a **captured** real-device observation, not
  only synthetic.
- At least one ambiguous/unsupported-device case exists and resolves to a **fail-closed** result —
  never something that could be mistaken for an approved install plan.
- Golden-output tests are deterministic (same evidence in, same plan out, byte-for-byte).
- Evidence provenance (where each fingerprint field came from) is visible in the output, not
  collapsed away.

## What Not To Build Yet

Originally written in at bootstrap per DR-0005 §3 and Seat 03's (Security) condition; **narrowed,
not deleted, by D-0040** (`corporate-strategy/decisions/DECISION_LOG.md`) once OS mutation was
authorized for the two named families:

- No OS mutation, driver-store write, elevation, or privileged process-exec for anything **outside
  the two D-0040-named families or outside its documented trust model** — not without its own
  separate future board/operator motion, regardless of how small the diff looks. This still
  includes: any additional family, any install mechanism other than the documented one, and any
  path that skips the required confirmation step.
- No fetching-and-executing a remote payload — D-0040's trust model explicitly excludes SpoolSmith
  downloading an installer from a network URL itself. Any change to add that is a new decision.
- No remote shell, no remote command execution beyond SpoolSmith's own local fingerprinting and
  its documented, visible install commands (PowerShell cmdlets / vendor's own signed installer).
- No credential handling or credential storage of any kind.
- No RMM-like workflows — SpoolSmith installs a driver a human already reviewed and approved,
  nothing else, and never on a schedule or in response to anything but an explicit, one-time
  confirmed command.
- **No fuzzy match ever installs anything silently, and no install ever skips the one required
  confirmation of a shown plan — this line survived D-0040 unchanged and is not up for
  negotiation by a future diff.** Confidence and evidence are always inspectable.
- No per-model hand-maintained catalog sprawl — if a change looks like "add model #500 to a giant
  table," the family/catalog abstraction has failed and that's a finding, not a feature.

## Driver-payload trust model (D-0040 — required to exist before install code, not after)

- Source: vendor-published installers only (HP UPD, Brother Full Driver Package). No mirrors.
- No network auto-fetch — installer must be staged locally or resolved via Windows'
  `Add-PrinterDriver`/`pnputil` against Windows Update/inbox drivers.
- Verification: Windows' own Authenticode signature check on the vendor EXE/MSI; `DriverPackage.SHA256`
  checked against the staged file as defense-in-depth when populated.
- Elevation: SpoolSmith does not self-elevate; fails closed if not run as Administrator.
- Approval: exactly one explicit confirmation of the full shown plan before any mutation, always.
- Rollback: `uninstall` reverses exactly what `install` recorded.

## Architecture Rules

- Go core, CLI first. Optional desktop surface is future roadmap, not current scope.
- Detection/normalization/catalog-resolution logic is pure and OS-independent; Windows
  install/mutation behavior lives behind clean, narrow OS-specific interfaces so the core is
  testable without Windows and without real hardware.
- Driver package/version/hash/signature metadata is a separate, explicitly swappable layer from
  the family catalog — volatile OEM download details must never leak into the stable
  identifier→family→catalog mappings.
- Evidence sources (SNMP, HTTP device-UI/model strings, PJL/JetDirect, open printing ports,
  MAC/OUI, hostname hints) are independent probes feeding one evidence-aggregation step; no probe
  should be load-bearing on its own for a driver decision without being visible in the output.

## NetViz integration boundary (DR-0005 §4 — read before adding any NetViz-facing code)

NetViz may hand SpoolSmith `HostObservation`-shaped evidence (IP, hostname, MAC/OUI vendor, open
ports, device-type guess) **and** invoke SpoolSmith for a **read-only preview/plan request only**.
SpoolSmith must never expose an invocation surface to NetViz (or anything else) that mutates
anything, locally or remotely — that stays gated exclusively by the OS-mutation prohibition above,
regardless of what calls it. SpoolSmith does not depend on NetViz existing; it gathers its own
fingerprinting evidence independently and must remain fully useful from its own CLI with zero
NetViz integration wired up.

## Commands

```sh
go build ./...
go vet ./...
go test ./... -v
go run ./cmd/spoolsmith inspect fixtures/hp-laserjet-m404-synthetic.json
```

## Coding Conventions

- Prefer small, explicit Go packages, mirroring netviz's own convention (`internal/scanner`
  isolation, typed events over callbacks).
- Every exported catalog-resolution function must be a pure function over its inputs — no hidden
  network calls, no hidden filesystem reads — so milestone one's acceptance tests can run
  anywhere, including CI, with zero real hardware.
- Add tests for: evidence normalization, ambiguous-match fail-closed behavior, and deterministic
  golden-output comparison, at minimum.
