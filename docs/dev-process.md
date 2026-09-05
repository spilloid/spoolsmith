# Dev Process Log

Records every round where implementation or review work is routed to Codex CLI (`codex exec`,
tiers Luna/Terra/Sol), so the routing policy below stays derived from measurements on *this* repo
rather than imported wholesale from another project. The tiering itself and the
orchestrator-drives/Codex-implements-and-reviews discipline are adapted from the pattern already
proven on player-2 (`docs/DEV-PROCESS.md`), PartnerCenterBridge, and AnchorDesk
(`docs/dev-process.md`) — see `corporate-strategy/standards/STD-001-adversarial-review.md` for the
company-wide history and its own honest caveats about how proven this pattern actually is. Claude
orchestrates (writes the spec/contract first, decides what to dispatch and at what tier, adjudicates
every finding itself); Codex CLI implements and reviews. **Adopted at bootstrap, before any code
exists, per DR-0005 §9** (`corporate-strategy/board/meetings/2026-09-03-spoolsmith-new-product/04-decision-record.md`)
— this is a deliberate, disclosed deviation from every other adopting repo's history, where the
routing table was written after real incidents already existed to derive rules from. This file
starts empty of log rows and accrues real entries from here forward.

## Routing

- **Luna** — mechanical, tightly-specified work: an established pattern to mirror, tests or an
  exact shape already given. Never shipped unreviewed.
- **Terra** — default implementer and default adversarial reviewer of anything Luna or the
  orchestrator wrote.
- **Terra/high** — escalation: nontrivial logic, more than a couple files, anything Terra/medium
  would be guessing on.
- **Sol/high** — architecture, security, concurrency, and anything touching:
  - privileged local execution, elevation, or a driver-store/spooler write of any kind
  - fetching a remote payload and executing or staging it, under any flag
  - the NetViz integration boundary (`CLAUDE.md`'s "NetViz integration boundary" section) —
    changing what NetViz may invoke or what evidence shape crosses that boundary

  regardless of diff size. **This is written in now, before any code exists that would trip it —
  per Seat 03 (Security)'s condition in the SpoolSmith board debate, specifically to avoid writing
  the rule under pressure from a diff someone already spent hours on.**

  **Update, D-0040:** driver-store writes for the two D-0040-named families, via its documented
  trust model, are now authorized — but the *routing tier for implementing and reviewing that
  code* is unchanged: still the highest tier this repo uses, implementer and reviewer both, no
  exception for the fact that the work itself is now allowed. Authorization and routing tier are
  two different questions; D-0040 only answered the first one.
- **Sol/ultra** — multi-file (5+) implementation or a broad review pass. As implementer, requires
  the operator's explicit in-session approval; never self-initiated.

These are starting priors adopted from the company-wide pattern, not derived from this repo's own
incidents yet — revise whichever rule the log below stops supporting, and say so when it happens.

## Standing rules carried over

None yet — this repo has no incident history. New rules get added here the same way every
adopting repo has done it: after a real defect, not speculatively. The one rule adopted in advance
rather than after an incident is the Sol/high trigger above, and it's flagged as such rather than
presented as if it came from this repo's own history.

## Log

| Unit | Task type | Author | Reviewer | Defects found | Defects real | Caught by tests instead | Est. tokens | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Milestone 1 fixture vertical slice | Multi-file implementation from written contract (`docs/milestone-1-spec.md`) | Codex (`codex exec -s workspace-write -c model_reasoning_effort=medium`) | Codex (`codex exec -s read-only -c model_reasoning_effort=high`), adjudicated by the orchestrator, who reproduced every finding directly before accepting it | 5 (3 High, 2 Medium) | 3 confirmed High by direct reproduction (fail-closed resolver defects — see below), 2 confirmed Medium (a test-rigor gap, and a documented-not-code architectural note not requiring a fix) | 0 — none of the 3 High findings were things the existing test suite caught; each was a gap *in* what the tests exercised | Not measured | **Initial implementation: fixed the wrong bar.** All 5 spec-listed acceptance commands passed (build/vet/test green), but the review found `catalog.Resolve` returns `Confidence: 1` / empty `Uncertain` for evidence that should fail closed — exactly the invariant this milestone exists to prove. Fixed same round (see next row); not shipped as originally implemented. |
| Milestone 1 fail-closed fix | Bugfix from written fix contract (`docs/milestone-1-fix-01.md`) | Codex (`codex exec -s workspace-write -c model_reasoning_effort=medium`) | Orchestrator, by direct re-execution of all three original repros plus the full suite (not re-dispatched to Codex a second time) | 0 new findings against the fix itself | N/A | New tests added for all 3 original repros plus a same-family/different-model case; golden-file comparison added for the determinism test (previously compared two in-process calls, which the review correctly flagged as not proving the spec's "separate runs" claim) | Not measured | **Fixed, independently verified.** All three original repros (conflicting models in one field; unsupported manufacturer named alongside a supported one; ambiguous multi-manufacturer MAC vendor string) now return `Family: nil, Driver: nil, Confidence: 0`, non-empty `Uncertain` — reproduced directly by the orchestrator, not accepted on Codex's report alone. Full suite green: `go build`, `go vet`, `go test ./... -v -count=1`. |
| CI/CD (GitHub Actions + manual script) | Mechanical build/release tooling from written contract (`docs/ci-cd-spec.md`), sequenced after the core unit per DR-0005 §8 | Codex (`codex exec -s workspace-write -c model_reasoning_effort=low`) | Orchestrator, by direct re-execution (not accepted on the dispatcher's report — see note below) | 0 defects in the created files | N/A | `go build`/`go vet`/`go test` re-run natively by both the dispatcher and the orchestrator, both green | Not measured | **Implemented and corrected.** Created `.github/workflows/{ci,release}.yml` (GitHub-hosted `windows-latest`/`ubuntu-latest` runners, not self-hosted — this repo has no self-hosted runner pool, unlike netviz's) and `scripts/build-release.ps1`. **The dispatcher's own report claimed the Windows cross-build failed** ("package internal/syscall/windows is not in std") and that it couldn't obtain a working toolchain. The orchestrator reproduced this directly instead of accepting the report: `GOOS=windows GOARCH=amd64 go build -o /tmp/spoolsmith-test.exe ./cmd/spoolsmith` succeeded cleanly and produced a real 4.5MB `PE32+ executable for MS Windows 10.00 (console), x86-64` binary. The dispatcher's failure claim was false in this environment as actually re-tested — exactly the "a sandboxed/restricted run can itself produce a false failure claim" pattern this company's own standing rules warn about, caught by direct reproduction rather than passed through. The PowerShell script itself still cannot be executed on this Linux sandbox (no PowerShell here at all) — that limitation is real and stays disclosed, distinct from the false cross-compile claim. |
| Milestone 2, Unit 1: live evidence collection (`internal/probe`) | Multi-file implementation from written contract (`docs/milestone-2-spec-probe.md`) — hand-rolled SNMP v1 BER encode/decode, HTTP scrape, PJL client, TCP port probe, OUI table, reverse DNS, no third-party deps | Codex (`codex exec -s workspace-write -c model_reasoning_effort=medium`) | Codex (`codex exec -s read-only -c model_reasoning_effort=high`), adjudicated by the orchestrator, who reproduced or independently confirmed every finding before accepting it | 5 (0 Critical/High, 2 Medium, 2 Low, 1 Low test-gap) | All 5 confirmed real by direct reproduction/verification — see fix row below | 0 — the existing suite didn't exercise the real concurrent `Collect()` path at all (the review's own 5th finding); the orchestrator independently ran `-race` against a real concurrent execution (5x, `127.0.0.1`, nothing listening) before the review even landed, and it was clean, but that check wasn't yet a permanent test | Not measured | **Implemented, reviewed, fixed same round.** The reviewer's own `-race` attempt failed in its dispatch sandbox (read-only `GOCACHE`) — it disclosed this plainly rather than claiming a result it didn't have, and reasoned from code structure instead (Go's memory model guarantee that distinct struct fields/slice elements written by different goroutines before a `sync.WaitGroup.Wait()` don't race). The orchestrator had already independently confirmed this by actual execution, not just inference. See fix row for the 5 confirmed findings and their resolution. |
| Milestone 2, Unit 1 fix | Bugfix from written fix contract (`docs/milestone-2-fix-01-probe.md`) | Codex (`codex exec -s workspace-write -c model_reasoning_effort=medium`) | Orchestrator, by direct re-execution (build/vet/test, `GOARCH=386` build, and `-race -count=5`) — not re-dispatched to Codex a second time | 0 new findings against the fix itself | N/A | New tests added per finding: SNMP request-ID randomness, BER oversized-length rejection without panic, non-minimal OID rejection, OID-specific SNMP value-type enforcement, cold-ARP retry, and a real concurrent `Collect()` race test | Not measured | **Fixed, independently verified, including on a 386 target.** SNMP request IDs now use `math/rand/v2` (31-bit) instead of a time-derived value, with an explicit comment that this closes the "trivially predictable" weakness but does not and cannot make SNMPv1 authenticated — inherent to the protocol, not this implementation, and evidence still flows through `catalog.Resolve`'s existing fail-closed logic before any human sees a plan. BER length parsing now rejects oversized/negative-wrapping lengths before any slice arithmetic (orchestrator independently confirmed the fix compiles and passes under `GOARCH=386`, the actual platform where the original defect would have manifested — it didn't reproduce on this repo's real amd64 targets, but the fix closes the latent defect anyway). OUI lookup now primes ARP resolution and retries the cache read twice on a cold miss. OID decoding rejects non-canonical encoding; SNMP value-type checks are now OID-specific (sysDescr must be OCTET STRING, sysObjectID must be OBJECT IDENTIFIER). All re-run directly by the orchestrator: `go build`, `go vet`, `GOARCH=386 go build`, `go test ./... -count=1`, and `go test ./internal/probe/... -race -count=5` — all clean. |

**Process note, disclosed rather than absorbed silently:** the first background dispatch of the
initial implementation unit was reported "completed" by the harness while its underlying `codex
exec` process was still actually running, unattended, against this same working tree — caught only
because the orchestrator checked `ps` directly rather than trusting the completion report, per this
company's own standing rule that a dispatcher's report is a claim, not proof. The stale process was
killed before it could race-write against a concurrently re-dispatched second attempt. No corrupted
files resulted, but this is exactly the failure class `corporate-strategy`'s root `CLAUDE.md`
already warns about, now confirmed to occur in this environment too.

**Environment note:** this sandbox had no Go toolchain at all before this unit — installed via
`brew install go` (1.27.1) to make any build/test/dispatch possible, mirroring the precedent set
in a prior corporate-strategy session that used `podman` to obtain a JDK for RetroSpool when no
JDK existed either.

The fixture-provenance acceptance condition is **still not satisfied**, and this is unchanged by
the fix above (the fix addressed resolver correctness, not evidence provenance). Neither supported
family has a captured real-device observation: nobody performing this implementation had access
to the printers in the sandbox. All supplied observations are labeled `synthetic` and identify
their public documentation sources and assumptions. Full "milestone one done" status remains
blocked until the operator captures evidence from actual printer hardware (plausibly at his MSP
job, per DR-0005 §10) and adds it as a truthfully labeled fixture; no synthetic fixture has been
presented as a capture.
| Milestone 2, Unit 2: Windows install automation (`internal/install`) | Multi-file implementation from written contract (`docs/milestone-2-spec-install.md`) — build-tag-split `Environment` seam, `BuildPlan`/`Preflight`/`Install`/`Uninstall`, PowerShell-only via native cmdlets (no vendor EXE invocation, no network fetch) | Codex (`codex exec -s workspace-write -c model_reasoning_effort=high`) | Codex (`codex exec -s read-only -c model_reasoning_effort=high`), adjudicated by the orchestrator, who verified the single most serious claim (PowerShell "smart quote" injection) directly against Microsoft's own `about_Quoting_Rules` documentation via WebFetch before accepting it | 6 (3 High, 3 Medium) | All 6 confirmed real — 3 by direct reproduction (smart-quote injection bypassing `powerShellString`; `WindowsDriverName`/package-label conflation), 3 by direct code reading against documented PowerShell/Go semantics | 0 — none of the 6 were things the existing test suite exercised; each was a gap in what the tests covered, same pattern as every prior review round in this repo | Not measured | **Implemented; review found the fail-closed promise itself was broken.** `powerShellString` didn't escape PowerShell's Unicode "smart quotes," which PowerShell treats as real string delimiters — reachable today via `uninstall <printer-name>`'s raw CLI argument, before any confirmation step. No generated command used `-ErrorAction Stop`, so a failed cmdlet could report success (PowerShell's default `$ErrorActionPreference` is `Continue`). `DriverPresent` discarded real errors, mislabeling infrastructure failures as "driver absent." Partial-mutation results were computed correctly (`Result.Ran`) but discarded by the CLI on any error path. Control characters in a value could make the *displayed* plan misleading even after the injection itself was closed. The Brother (and unverified HP) "driver name" was a marketing package label, not a confirmed `Get-PrinterDriver` identity. Fixed same round — see next row. |
| Milestone 2, Unit 2 fix + addendum | Bugfix + 3-feature upgrade from written contract (`docs/milestone-2-fix-01-install.md`), folded into one round per the operator's own request | Codex (`codex exec -s workspace-write -c model_reasoning_effort=high`) | Orchestrator, by direct re-execution (build/vet/test, `GOOS=windows` cross-build, `GOARCH=386` build, `-race`) plus two hand-written repro tests for the two High findings, run before and independently of the dispatcher's own test suite | 0 new findings against the fix itself | N/A | New tests per finding (smart-quote injection string, non-terminating-error wrapper, driver-check error preservation, partial-failure surfacing, 5 control-character variants including bidi isolates, `WindowsDriverName`-empty fail-closed) plus full addendum coverage (`catalog families`, `--force-family` valid/invalid, interactive picker abort/select, `--dry-run`/`--what-if` never-mutates across every preflight outcome, the full exit-code contract per branch, stdout-is-JSON/stderr-is-interactive separation) | Not measured | **Fixed and extended, independently verified — including re-deriving my own verification tests after finding my first two verification attempts were themselves buggy** (checked for the escaped substring's *presence* rather than whether it was actually preceded by the escaping backtick, then didn't exclude the closing string delimiter either) — corrected on the second/third attempt, confirmed the injection is genuinely closed and `WindowsDriverName`-empty genuinely fails closed. `go build`, `go vet`, `GOOS=windows GOARCH=amd64 go build`, `GOARCH=386 go build`, `go test ./... -count=1`, and `go test ./internal/install/... ./internal/probe/... -race -count=1` all clean. **Both `WindowsDriverName` fields remain intentionally empty** — real installs fail closed until the operator stages each genuine vendor package on Windows, runs `Get-PrinterDriver`, and populates both strings. That, plus real hardware execution of `windows.go` itself, are the two things this sandbox cannot close. |

# Platform follow-ups

- Live OUI probing currently reads Linux's `/proc/net/arp`. A Windows-native
  neighbor-cache reader is intentionally deferred beyond milestone 2, unit 1.
- **Real Windows driver names are the actual remaining blocker for install to run at all.**
  `catalog.DriverPackage.WindowsDriverName` is empty for both HP and Brother by design — populate
  both (stage the real vendor package on a Windows machine, run `Get-PrinterDriver`, copy the exact
  registered name) before attempting a real install against either family.

## Real CI, found and fixed the same day it started running

`ci.yml`'s `push` trigger had actually been running since the CI/CD unit landed, and had been
**failing on `windows-latest`** on both commits since — this went unnoticed through two full
review-and-fix rounds because nobody checked `gh run list` until preparing to cut the v0.1.0
release. Caught then, not before: `TestInspectDeterministicGoldenOutput` failed on Windows only.
Root cause: `internal/inspect/testdata/*.json` were checked in with LF line endings (everything
here was authored on Linux) with no explicit line-ending attribute, so the `windows-latest`
runner's git checkout converted them to CRLF on checkout, breaking the exact-byte comparison that
same test was deliberately strengthened to perform last round (see the milestone-1-fix-01 log row
above — the fix for a different finding is what made this one visible). Fixed with a repo-root
`.gitattributes` forcing `eol=lf` for text files (`.ps1` left CRLF-native, since nothing byte-
compares those). No blob renormalization was needed — the stored content was already LF; only the
attribute declaration was missing. Re-pushed and watched the real run (`gh run watch`) go green on
both `windows-latest` and `ubuntu-latest` before treating this as closed — not assumed from the
diff alone.

## v0.1.0 cut and verified for real (2026-09-05)

Tagged and released `v0.1.0` — the first real, tagged release this repo has ever produced.
Watched the actual `Release Builds` workflow run live (`gh run watch`) rather than assuming it
worked because the trigger fired: `windows-amd64` build succeeded, the zip + sha256 uploaded
cleanly. Then independently re-verified outside the workflow entirely — downloaded the actual
release asset, confirmed the checksum matches, unzipped it, and confirmed the contained
`spoolsmith.exe` is a genuine `PE32+ executable for MS Windows... x86-64` binary, not just an
"uploaded" status in the API. Release notes state plainly that `install` is fully built but
intentionally refuses to run for either family pending real-hardware driver-name verification —
the same disclosure as everywhere else in this log, not softened for a public release.

Also rebuilt `docs/` as a real static product page (`index.html` + `.nojekyll`) matching the
established Spillers Technology portfolio convention (checked netviz's and the storefront's own
`docs/index.html` directly rather than guessing at the house style), replacing an earlier
Jekyll-themed draft that didn't match how every sibling repo actually does this.
