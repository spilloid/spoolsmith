# CLAUDE.md

## Project Mission

SpoolSmith identifies network printers, resolves the correct OEM driver through a small,
family-oriented catalog, presents a reviewable install plan, and installs/configures the printer
locally only after explicit user approval. It is a standalone product with its own CLI, its own
driver catalog, and its own OS-mutation logic — not a NetViz feature, not an RMM.

Governance: authorized by `corporate-strategy` board decision DR-0005
(`board/meetings/2026-09-03-spoolsmith-new-product/04-decision-record.md`,
`decisions/DECISION_LOG.md` D-0039) as a **milestone-one authorization, not a product
authorization**. See `corporate-strategy/state/products/SpoolSmith.md` for current status,
budget, and revisit triggers before assuming any scope beyond what's written here.

## Current scope: milestone one only

A fixture-driven vertical slice, nothing more:

1. Normalize captured/synthetic printer fingerprint evidence into an observed-identifier set.
2. Resolve a normalized model, then a printer family, from those identifiers.
3. Resolve a driver package/strategy for that family.
4. Emit a deterministic `inspect`/install-plan result: manufacturer/model, evidence, normalized
   family, selected driver + source, confidence, and anything uncertain — inspectable, never
   silent.

**No OS mutation. No remote execution. No credential collection.** This milestone proves the
abstraction chain against fixtures; it does not touch a real Windows driver store.

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

Written in from bootstrap, per DR-0005 §3 and Seat 03's (Security) condition in
`corporate-strategy/board/meetings/2026-09-03-spoolsmith-new-product/01-debate.md` — mirroring
netviz's own pattern in `netviz/CLAUDE.md`:

- No OS mutation, driver-store write, elevation, or privileged process-exec of any kind, under
  any flag (including a "test-only" or "dry-run" one that could grow a production caller) — not
  without a **separate future board/operator motion**, regardless of how small the diff looks.
- No fetching-and-executing a remote payload without a written driver-payload trust model (source,
  signature/hash verification, elevation scope, rollback/uninstall path) existing *first*, and
  that trust model itself requires board/operator sign-off before the install-milestone motion
  that would need it.
- No remote shell, no remote command execution beyond SpoolSmith's own local fingerprinting.
- No credential handling or credential storage of any kind.
- No RMM-like workflows — SpoolSmith installs a driver a human already reviewed, nothing else.
- No fuzzy match ever installs anything silently. Every install plan is reviewable before
  approval; confidence and evidence are always inspectable.
- No per-model hand-maintained catalog sprawl — if a change looks like "add model #500 to a giant
  table," the family/catalog abstraction has failed and that's a finding, not a feature.

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
