# Milestone 1 — Implementation Spec

Written by the orchestrator before dispatch, per STD-001's "orchestrator writes the contract
first" discipline. This is the contract an implementer is dispatched against; it is not a
suggestion to reinterpret.

Authorized by DR-0005 (`corporate-strategy/board/meetings/2026-09-03-spoolsmith-new-product/04-decision-record.md`).
Scope boundary: see `CLAUDE.md` — no OS mutation, no remote execution, no credential collection,
no live network probing in this unit. This unit operates entirely on evidence already captured
into fixture files; it proves the abstraction chain, not device connectivity.

## Goal

`observed identifiers -> normalized model -> printer family -> driver package/strategy`, as a
pure, deterministic, testable Go library plus a thin CLI, over exactly two real-world printer
families.

## Package layout (create exactly this; do not invent a different shape)

```
spoolsmith/
  go.mod                          module github.com/spilloid/spoolsmith, Go 1.22+
  cmd/
    spoolsmith/
      main.go                     CLI entrypoint
  internal/
    evidence/
      evidence.go                 Evidence struct + fixture loader
      evidence_test.go
    catalog/
      family.go                   Family, family registry (2 families, hand-written, small)
      driver.go                   DriverPackage / DriverStrategy — SEPARATE from family.go
      resolve.go                  Evidence -> normalized model -> Family -> DriverPackage
      resolve_test.go
    inspect/
      inspect.go                  Assembles the InspectResult; the "business logic" of `inspect`
      inspect_test.go
  fixtures/
    hp-laserjet-m404-captured.json       CAPTURED, real device, provenance below
    hp-laserjet-m404-synthetic.json      synthetic variant, informed by public HP docs
    brother-hl-l2350dw-synthetic.json    synthetic, informed by public Brother docs
    ambiguous-unknown-vendor.json        deliberately unresolvable — must fail closed
  docs/
    milestone-1-spec.md             this file
```

## Types (exact shapes; implementer may add fields but must not remove or rename these)

```go
// internal/evidence/evidence.go
package evidence

type Evidence struct {
    IP              string            `json:"ip,omitempty"`
    SNMPSysDescr    string            `json:"snmp_sys_descr,omitempty"`
    SNMPSysObjectID string            `json:"snmp_sys_object_id,omitempty"`
    HTTPTitle       string            `json:"http_title,omitempty"`
    HTTPModelString string            `json:"http_model_string,omitempty"`
    PJLID           string            `json:"pjl_id,omitempty"`
    OpenPorts       []int             `json:"open_ports,omitempty"`
    MACVendor       string            `json:"mac_vendor,omitempty"`
    Hostname        string            `json:"hostname,omitempty"`
    Provenance      string            `json:"provenance"` // "captured" or "synthetic" — MANDATORY, no default
    ProvenanceNote  string            `json:"provenance_note,omitempty"` // required when provenance == "synthetic": what real-world source informed it
}

func LoadFixture(path string) (Evidence, error)
```

`Provenance` is not decorative. `LoadFixture` must return an error if `provenance` is missing or
is any value other than `"captured"`/`"synthetic"`. A "synthetic" fixture with no
`provenance_note` is also a load error. This is the mechanism that makes Seat 06's
captured-vs-synthetic condition mechanically enforced, not just documented.

```go
// internal/catalog/family.go
package catalog

type Family struct {
    ID           string   // e.g. "hp-laserjet-m4xx"
    Manufacturer string   // "HP"
    Aliases      []string // normalized-model strings that map to this family, e.g. "HP LaserJet Pro M404", "HP LaserJet Pro M404dn", "HP LaserJet Pro M404n"
}

// Families returns the fixed, hand-written registry for this milestone. Exactly two
// families. Adding a third is out of scope for this unit.
func Families() []Family
```

```go
// internal/catalog/driver.go — DELIBERATELY SEPARATE FILE from family.go.
// Driver metadata is volatile (URLs, versions, hashes); family identity is stable.
// This split is not optional — it's the architectural point of this milestone.
package catalog

type DriverPackage struct {
    FamilyID    string
    Name        string
    Source      string // e.g. "HP Universal Print Driver (PCL 6)" — a name/description, not a live URL fetch in this unit
    Version     string
    SHA256      string // may be empty in this unit (no real download happens) — but the FIELD exists so milestone 2 has somewhere to put it
    Strategy    string // e.g. "vendor-universal-pcl6" — a strategy identifier, not executable code
}

func DriverFor(familyID string) (DriverPackage, bool)
```

```go
// internal/catalog/resolve.go
package catalog

type ResolutionResult struct {
    NormalizedModel string
    Family          *Family        // nil if unresolved
    Driver          *DriverPackage // nil if unresolved or family has no mapped driver
    Confidence      float64        // 0.0-1.0
    Uncertain       []string       // human-readable list of what's uncertain/ambiguous — MUST be non-empty whenever Confidence < 1.0
}

// Resolve is pure: same Evidence in, same ResolutionResult out, every time. No I/O.
func Resolve(e evidence.Evidence) ResolutionResult
```

**Fail-closed rule, mechanically enforced, not a comment:** if `Resolve` cannot confidently match
a family (ambiguous evidence, conflicting signals, or a vendor/model with no fixture-backed
mapping), it MUST return `Family: nil, Driver: nil, Confidence: <1.0` with a non-empty
`Uncertain`. It must never return a low-confidence *non-nil* Family/Driver pair that a caller
could mistake for an approved plan by only checking for non-nil. Test this explicitly with
`ambiguous-unknown-vendor.json`.

```go
// internal/inspect/inspect.go
package inspect

type InspectResult struct {
    Manufacturer    string                     `json:"manufacturer,omitempty"`
    Model           string                     `json:"model,omitempty"`
    Evidence        evidence.Evidence          `json:"evidence"`
    NormalizedModel string                     `json:"normalized_model"`
    Family          *catalog.Family            `json:"family,omitempty"`
    Driver          *catalog.DriverPackage      `json:"driver,omitempty"`
    Confidence      float64                    `json:"confidence"`
    Uncertain       []string                   `json:"uncertain,omitempty"`
}

func Inspect(e evidence.Evidence) InspectResult
```

## CLI

```
spoolsmith inspect <fixture.json>
```

Reads the fixture, runs `inspect.Inspect`, prints `InspectResult` as indented JSON to stdout.
Non-zero exit code if the fixture fails to load (bad/missing provenance, malformed JSON, file not
found) — with a clear stderr message. This is the only CLI command in this unit. No `catalog
probe`, no `install`, no network access anywhere in this binary.

## The two families (real, not invented)

1. **HP LaserJet Pro M4xx family** — real HP consumer/SMB laser printer line. Driver strategy:
   HP's Universal Print Driver (PCL 6) is the real, documented HP recommendation for this family
   (HP publishes UPD as covering the M4xx line) — cite this as the `DriverPackage.Source` string.
2. **Brother HL-Lxxxx family** — real Brother laser printer line. Driver strategy: Brother
   publishes model-specific "Full Driver & Software Package" installers per model but groups
   support by a shared "seriess" driver architecture for several HL-L2xxx models — cite the
   family grouping honestly and narrowly (don't overclaim a single universal Brother driver the
   way HP's UPD actually is universal — that asymmetry between the two vendors' real driver
   strategies is worth the fixture reflecting, not smoothing away).

## Fixture provenance — write this honestly, do not fabricate "captured" data

**`hp-laserjet-m404-captured.json` cannot actually be marked `"provenance": "captured"` by this
implementation unit** — nobody on this task has a real HP LaserJet M404 to run `snmpwalk`/PJL
queries against inside this sandboxed environment. Do not fabricate a fake capture and label it
"captured." Instead:

- Name the file `hp-laserjet-m404-synthetic.json` (drop the `-captured` variant from the file list
  above if no real capture exists — the spec's file list is a target, not a permission to
  fabricate one entry to hit it).
- Base its field values on real, publicly documented HP identification strings where you can find
  them (HP's SNMP `sysDescr` format for LaserJet printers, HP's PJL `@PJL INFO ID` response
  format) and cite what you based it on in `provenance_note`.
- **State explicitly, in the PR/commit description and in `docs/dev-process.md`'s log row, that
  the "at least one captured, not-only-synthetic fixture per family" condition from the board's
  Phase 2/3 debate (Seat 06) is NOT satisfied by this unit** — it remains open, blocking full
  "milestone one done" status, until the operator captures real evidence from an actual printer
  (plausibly at his MSP job, per DR-0005 §10) and it's added as a real fixture. This is a known,
  disclosed gap, not something to paper over with a mislabeled fixture.

## Acceptance tests (must all pass; this is the actual "done" bar, not the file list above)

1. `catalog.Resolve` on both real families' synthetic fixtures returns the correct
   `Family.ID`/`DriverPackage` with `Confidence == 1.0` (or documented near-1.0 if evidence is
   deliberately partial) and empty `Uncertain`.
2. `catalog.Resolve` on `ambiguous-unknown-vendor.json` returns `Family: nil, Driver: nil` and a
   non-empty `Uncertain`, `Confidence < 1.0`.
3. Golden-output test: `inspect.Inspect` on each fixture produces byte-identical JSON across two
   separate runs (true determinism, not "usually the same").
4. `evidence.LoadFixture` rejects a fixture missing `provenance` and rejects a `"synthetic"`
   fixture missing `provenance_note`, both with a clear error.
5. `go vet ./...` and `go build ./...` clean. `go test ./... -run . -v` all green.

## Explicitly out of scope for this unit (do not build, even if it looks easy)

- Any live network call (SNMP, HTTP, PJL-over-9100) — fixtures only.
- `catalog probe`, `install`, or any second CLI subcommand.
- A third printer family.
- Any Windows-specific code at all.
- CI/CD (GitHub Actions, release scripts) — DR-0005 §8 explicitly sequences that after this unit
  passes acceptance, as a separate dispatch.
