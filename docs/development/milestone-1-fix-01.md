# Fix spec — three confirmed fail-closed defects in internal/catalog/resolve.go

Found by an independent adversarial Codex review (`codex exec -s read-only -c
model_reasoning_effort=high`), each reproduced directly by the orchestrator before this fix was
authorized. All three currently return `Confidence: 1, Uncertain: []` for evidence that should
fail closed — this is the exact invariant the board's decision record (DR-0005 §6) named as
non-negotiable ("no fuzzy match ever installs anything silently"), so these are not stylistic
findings; fix all three.

## Finding 1 — a single field naming two supported models resolves to one of them

Repro: `HTTPModelString: "HP LaserJet Pro M404dn and Brother HL-L2350DW"` resolves to HP,
confidence 1, empty Uncertain. `matchIdentity` finds both aliases inside the one field, keeps only
the longest match, and discards the other — so `Resolve` never sees the second family at all.

**Fix:** change `matchIdentity`'s contract so a single field can report *every* distinct alias hit
it contains, not just the longest one. Concretely: change its signature to return `[]modelMatch`
(all aliases found in `value`, deduplicated only when they are the exact same family+model — never
deduplicated across different families or different models within the same family), and have the
caller in `Resolve` append every returned match into the aggregate `matches` slice for that field,
the same way it already does across different fields. Do not add new conflict-detection logic
here — the existing aggregate checks (`len(familyIDs) != 1`, `len(models) != 1`) already fail
closed correctly once both aliases actually reach the `matches` slice; the bug is that they never
did.

## Finding 2 — an unsupported manufacturer named alongside a supported one is invisible

Repro: `HTTPModelString: "HP LaserJet Pro M404dn"`, `PJLID: "MFG:Canon;MDL:imageCLASS
LBP6230dw;CLS:PRINTER;"` resolves to HP, confidence 1, empty Uncertain. Only catalog aliases are
ever considered; a manufacturer name that isn't HP or Brother contributes nothing, so the
contradiction is silently dropped rather than detected.

**Fix:** add a small, static, stable list of well-known printer-manufacturer name tokens — HP,
Hewlett Packard, Brother, Canon, Epson, Lexmark, Xerox, Kyocera, Ricoh, Konica Minolta, Dell,
Samsung, OKI, Sharp — used *only* to detect a foreign-manufacturer mention, never to resolve a
family (this is a manufacturer-name list, not a per-model database, and stays that way: no model
numbers, no aliases, no driver mappings attached to it). Specifically parse the PJL `MFG:` field
when present (`MFG:<name>;` is the standard PJL INFO ID/USTATUS syntax) as a structured signal,
and also scan `SNMPSysDescr`, `HTTPTitle`, `HTTPModelString` for a manufacturer-name token as a
looser signal. If any evidence field names a manufacturer from this list that differs from the
manufacturer of an otherwise-matched family, `Resolve` must return unresolved (nil Family, nil
Driver, Confidence < 1, non-empty Uncertain) — the same way the existing `MACVendor` check already
does for that one field. Reuse `vendorMatches`'s shape/spirit rather than inventing a third
pattern, but see Finding 3 below before reusing its substring logic as-is.

## Finding 3 — the MAC-vendor conflict check itself is too permissive

Repro: `HTTPModelString: "HP LaserJet Pro M404dn"`, `MACVendor: "Brother Industries / HP"`
resolves to HP, confidence 1, because `vendorMatches` only checks whether the string *contains* the
token `HP` — it doesn't notice the string also names Brother.

**Fix:** `vendorMatches` (and any new manufacturer-mention check from Finding 2) must fail closed
when a vendor/manufacturer-bearing string names *more than one* known manufacturer, not just check
whether it contains the expected one. Concretely: scan the string for every manufacturer name from
the Finding 2 list that appears in it; if more than one distinct manufacturer is named, that's
itself a conflict — return unresolved — even before comparing against the matched family's
manufacturer.

## Required new tests (in addition to fixing the existing ones, which must still pass)

- A case matching Finding 1's exact repro, asserting nil Family/Driver, non-empty Uncertain.
- A case matching Finding 2's exact repro, asserting nil Family/Driver, non-empty Uncertain.
- A case matching Finding 3's exact repro, asserting nil Family/Driver, non-empty Uncertain.
- A case confirming a single field containing two *aliases of the same family* (e.g. "M404" and
  "M404dn" both appearing) does NOT falsely conflict if they resolve to the same model, and DOES
  return unresolved (per the existing `len(models) != 1` check) if they name two different models
  of the same family.

## Also fix, lower severity (Medium, from the same review)

**Determinism test doesn't prove what it claims.** `inspect_test.go`'s
`TestInspectDeterministicGoldenOutput` currently calls `Inspect` twice in the same process, which
cannot catch a value that's chosen once per-process (e.g. via map iteration or `init()`) but
differs across separate process runs. Strengthen it: write each fixture's expected JSON output to
a checked-in golden file under `internal/inspect/testdata/`, and compare the live `Inspect` output
against that stored golden file's bytes exactly, rather than against a second in-process call. This
is what "byte-for-byte across two separate runs" in the spec actually requires mechanically.

**Do not touch:** `cmd/spoolsmith/main.go`'s conventional-not-enforced mutation boundary (review
Finding 5) — that's an accepted, disclosed architectural note for a future unit, not a defect in
this one; the CLI currently performs no mutation and nothing in this fix should add any.

## Verification required before reporting done

`go build ./...`, `go vet ./...`, and `go test ./... -v -count=1` all clean, including the new
tests above. Do not report this fix complete without pasting the actual test output showing the
new fail-closed tests passing.
