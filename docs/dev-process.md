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
