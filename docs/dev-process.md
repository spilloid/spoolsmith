# Dev Process Log

## 2026-09-06: native GUI (feature parity) + action-log observability, Claude picks up after Codex ran out

Codex hit its usage limit mid-GUI-split (see the release checkpoint below); the
operator asked Claude to continue past `v0.2.0` toward a native GUI reaching CLI
feature parity, plus per-action logging to a local temp file for observability.
Recorded as `corporate-strategy` D-0042 (direct operator direction, no board
review — a new front-end over already-authorized D-0039/D-0040/D-0041
capabilities, not an OS-mutation scope change). Spec: `docs/milestone-3-spec-gui.md`.

**`internal/actionlog`:** a small, independently-tested JSON-lines logger
(`os.TempDir()/spoolsmith/actions.log`, 5 MB single-rotation, nil-safe, degrades to
a one-time stderr warning and a no-op logger rather than ever blocking a command).
Wired into the CLI at exactly one point — `main()` wrapping `run()`'s result — so
none of the already-reviewed command logic or its tests needed touching. Verified
end-to-end with real CLI invocations, not just unit tests: ran `catalog families`
and `inspect` against the built exe and confirmed both lines landed in the real
temp-directory log file.

**`cmd/spoolsmith-gui`:** imports `internal/{probe,catalog,inspect,install}`
directly and calls the same `install.Workflow.RunInstall`/`RunUninstall` the CLI
calls — no shelled-out CLI invocation, no second mutation path. The confirmation
gate reuses the CLI's own already-tested branches rather than reimplementing
terminal I/O: **Preview** always forces `DryRun: true` (byte-identical plan/preflight
text to `--dry-run`, never reaches a mutating call); **Execute** is only
clickable after a successful Preview, itself requires a native Yes/No `MsgBox`
naming the operation, and only then re-runs the identical call with
`Yes: true, NonInteractive: true` (the CLI's own `--yes --non-interactive`
contract). Feature parity covers `discover`, `inspect`, `catalog
families`/`probe`, `profile capture`/`edit`, `install`/`add`/`configure`
(`--force-family`, `--profile`), and `uninstall`/`remove` (`--purge-driver`,
`--profile`).

**Real defect found and fixed before this could be called done, not asserted from
reading the code:** built the GUI against `lxn/walk` (the library Codex had already
started evaluating) and it crashed on first launch on this actual Windows 11
machine — `TTM_ADDTOOL failed`, a `TOOLINFO`-struct/comctl32-version mismatch — the
moment any tooltip-capable widget (i.e., basically anything) is created. Confirmed
this wasn't a manifest problem (added the standard comctl32-v6 side-car manifest
used by `lxn/walk`'s own examples; crash persisted). `lxn/walk` has had no commits
since 2021 (confirmed via the module proxy's own `@latest` resolution — not
assumed from a stale-looking repo). Switched to `github.com/tailscale/walk`, a
maintained fork Tailscale runs their own Windows GUI on that fixes exactly this;
the swap was a mechanical import-path change (same package shapes, `MainWindow`/
`declarative` API compatible) plus raising this module's `go` directive and both
CI legs' pinned Go version from 1.22 to 1.24 to match the fork's own `go.mod`.
Re-verified by actually launching the rebuilt exe (not just a clean `go build`):
it now starts, stays resident, and a real screenshot (`Start-Process` +
`CopyFromScreen`) shows the Discover tab's label/input/button/output layout
rendering correctly.

Also extended `.github/workflows/release.yml` to build and package
`spoolsmith-gui.exe` (and its side-car manifest) alongside `spoolsmith.exe` in the
release zip, since a released "GUI with CLI parity" that only ships the CLI binary
would not actually be shipped.

**What this round did not verify:** clicking through Discover/Inspect/Catalog/
Profiles/Install/Uninstall interactively (only the widget tree render was
confirmed by screenshot; the underlying calls are the CLI's own tested code
paths, not re-tested through simulated GUI clicks). No real install/uninstall
mutation was exercised through the GUI on this machine — that stays consistent
with this repo's own caution about not exercising real driver-store writes
outside a deliberate, disclosed session. `TestLocalBrotherArchiveVerificationWithStagingDoubles`
in `internal/install` fails on this machine on unmodified `main` too (confirmed by
stashing this session's changes and re-running it) — a real, pre-existing,
environment-specific defect (this machine's real `tar.exe`/PowerShell behavior vs.
the test's fake environment), not something this session introduced or fixed.

## 2026-09-06: release checkpoint before native GUI

The operator requested a PR, merge, and release before continuing the GUI split.
Only GUI dependencies had been added; saved those manifests in ignored local scratch
and removed them from this checkpoint. v0.2.0 remains a dependency-free Windows CLI.
Prepared release notes covering verified hardware output and the limits of package
automation. Native GUI work will start from the released baseline and reuse the
CLI's core validation, planning, confirmation, and execution paths.

## 2026-09-06: physical print confirmed, local package recipe, product site refresh

The operator reported sending a test print to Brother Home and watching it work.
Recorded that physical-output confirmation in the package record, ignored local
profile, README, hardware runbook, and current product state.

Added an optional profile package reference (`id`, local `archive`) and edit/clear
commands. The reviewed Brother source record is embedded independently of device
identity. One shown plan/confirmation now covers local package staging and mapping.
Existing drivers bypass archive access. A missing driver requires Windows x64,
pinned SHA-256, valid Brother signature, safe archive entries, valid Microsoft driver
catalog, successful INF staging and verified driver registration. The archive is
held read-only during checks/extraction. No vendor EXE execution or network fetching
was added. Extraction directories remain in Windows temp for diagnostics; partial
staging is not rolled back. Dry-run previews without validating/extracting payloads.

The real local archive test exercises Windows hashing, signatures, and tar, while
replacing pnputil/Add-PrinterDriver with in-memory doubles. It caught a certificate
subject quoting mismatch; corrected this to compare the parsed X509 simple name.
Verification now passes, including reapply without a second staging call. Other
tests cover invalid package/driver combinations, relative paths, edit/clear conflicts,
dry-run/confirmation precedence, staging failure stopping queue commands, existing
driver no-op, and hash mismatch. Full `go test ./...`, `go vet ./...`, and binary
build passed. This is not a clean-machine live install claim for the new automation.

The operator requested a stronger product pass on the GitHub Pages site. Replaced
the architecture/governance-led landing page with daily-use benefits, a printer SVG,
find/save/reuse workflow, repeat-add example, and three interactive starting points.
Copy explicitly identifies the Windows CLI, current source workflow, prerequisites,
and limited package coverage. Removed obsolete claims that install is inert and
avoided promising that older published releases contain the new profile features.

Local headless Chrome checks passed at widths 1440, 1024, 768, 390, and 320 with no
horizontal overflow. Internal anchors, click/keyboard tabs, clipboard success and
denial fallback passed with no browser exceptions. Inspected desktop/mobile PNGs.
Site uses local CSS/JS and an inline SVG; no runtime dependencies or external fonts.
Screenshots and the browser harness remain ignored under `profiles/.site-check/`.
Changes are local; no release, push, or Pages deployment was performed.

Attached the recipe to the ignored Brother Home profile through `profile edit`
(with backup). Its live dry-run passed, including matching evidence and registered
driver presence. No additional printer-state writes or physical test pages were
needed. A Claude Opus read-only review was attempted, then retried with network
access after stalling; neither returned review output. Both owned processes were
stopped. No completed independent review is claimed for this follow-up.

## 2026-09-06: supplied Brother package closes real mapping blocker

The operator supplied Brother's direct download URL for
`Y14A_C1-hostm-1110.EXE`. Downloaded it from `download.brother.com`; Windows
Authenticode reported a valid Brother Industries signature. The SHA-256 of the
observed file is `6814e22081074524ab08b687afb5965b3577aee1a40d97fc39012bf758b8a0ae`.
Windows `tar` listed and extracted the archive without executing its EXE.
`32_64/BROHL13A.INF` declares version 1.11.0.0 dated 2016-10-18 and explicitly
lists `Brother HL-L2315D series` for NTx86 and NTamd64. Its catalog signature is
valid, signed by Microsoft Windows Hardware Compatibility Publisher.

After the reviewed driver-store action was approved, `pnputil /add-driver` staged
the package as `oem15.inf`, and `Add-PrinterDriver` registered the exact model name
on Windows x64. This name is model-specific and was not assigned to the entire
Brother family. The ignored home profile was updated through `profile edit`,
preserving its previous version. Its live dry-run then passed every preflight.

After approval of the displayed queue/endpoint/driver plan, actual SpoolSmith
`add --profile ... --yes --json` created the Brother Home queue and RAW TCP 9100
port. Repeating the exact operation returned `Unchanged port` and `Unchanged
printer`. Independent `Get-Printer`/`Get-PrinterPort` reads confirmed the driver,
port association, target, protocol 1, and port number 9100. This closes real Windows
add/reapply verification for this printer. Removal remains covered by the real
PowerShell doubles harness, not a live removal in this session. No physical test
page has been sent or observed.

`catalog/packages/brother-y14a-c1.json` records the source, hash, signed INF and
verified model entry separately from device identity/profile data. It is a source
record, not yet consumed as an automatic package-install recipe by the CLI. The
supplied stable URL is not treated as proof that future payload bytes are identical.
Downloaded vendor payloads and local inventory remain under ignored `profiles/`.

## 2026-09-06: operator-directed daily-use workflow (Astra implements, Claude reviews)

The operator asked Astra to push practical workplace use: known-IP mapping,
discovery, and reusable JSON per printer, then explicitly emphasized idempotency,
strong UX, and easy add/remove/config. D-0041 records this operator direction;
`docs/daily-use-spec.md` is the implementation contract. This is a disclosed
assignment change from the original routing priors below, authorized in-session
by the operator. No board vote or portfolio-status change is claimed.

Implemented bounded IPv4 CIDR discovery, versioned declarative profiles, live
HTTP/PJL continuity checks, and mapping with an operator-selected installed driver.
The mapping plan retains driver-presence/elevation checks and confirmation. Profiles
extend manual mapping beyond the two built-in automatic catalog families without
claiming automatic driver compatibility. Capture never overwrites inventory;
profile edits preserve backups. Local `profiles/` inventory is ignored by Git.

Claude Opus's first independent read-only review confirmed the core safety path
and identified real gaps: repeat install failed on existing ports, the new scope
was not reflected in governing documents, LPD candidates were never probed, and
a global Ctrl-C handler would trap input at confirmation. Those findings were
adjudicated against code and fixed: guarded queue/port reconciliation, D-0041 and
scope notes, port 515 probing plus known-catalog identity candidates, and a
discovery-local signal handler. Identity diagnostics now name fields and quote
saved/current values. The review's no-CLI-tests observation was stale by arrival;
CLI tests had landed while the review was running. Bad profile syntax still uses
exit 2 plus usage as a CLI-input error; this is retained deliberately.

Following the operator's UX clarification, `add`, `configure`, `remove`, and
`profile edit` provide explicit workflows. Matching mappings execute no mutating
cmdlets; queue changes require configure; conflicting ports are never overwritten.
Removal retains shared resources and external ports, and already-absent queues
succeed without prompting. Terminal plans are concise; redirected/`--json` output
preserves full commands and metadata. A failure between operations is still not a
transaction: retrying add can reuse an orphan port; no automatic rollback is claimed.

Verification so far: `go test ./...`, `go vet ./...`, and Windows executable build
passed after the UX/idempotency changes. The regression suite executes actual
PowerShell with in-memory cmdlet doubles for repeat add/remove, explicit configure,
endpoint/queue conflicts before mutations, and shared-port retention. No real
spooler mutation occurs in those tests. The first conflict-test assertion was itself
wrong: PowerShell echoed the whole submitted script in an error record, so searching
that record for a sentinel found its definition rather than an executed mutation.
The harness now renders the actual exception message and the corrected tests pass.
Race-detector execution has not been claimed; this PC has no `gcc` on PATH.

Real hardware: a sandboxed /32 scan returned no candidate; repeating outside the
network sandbox found the existing Brother HL-L2315D with HTTP/PJL/SNMP evidence.
The empty sandbox result was not reported as an absent printer. Read-only Windows
driver inventory found Microsoft class drivers only, no Brother/HP OEM driver.
An ignored home-printer draft profile contains the real captured evidence and an
explicit replacement placeholder for the unstaged OEM driver name. Actual profile
queue mapping/unmapping and driver-package acquisition/staging remain unverified
and unimplemented respectively. No printed test page or release is claimed.

The second Claude Opus review confirmed that the guarded reconciliation, quoting,
confirmation and PowerShell execution tests hold. Confirmed findings fixed in the
same round: compact plans now retain manual-selection/source/uncertainty disclosures;
profile removal cross-checks the installed port and driver; configure requires a
profile; backups moved to `.backups/*.bak` outside JSON globs; alias outcomes/errors
use the invoked command name. A live dry-run exposed intermittent missing HTTP
evidence, so one missing model probe is now tolerated when another saved model probe
agrees; conflicts still fail immediately and unavailable identity retries only once.
SNMP-only captures are supported explicitly, without allowing SNMP to substitute
for saved HTTP/PJL sources. New tests cover these changes.

The review's proposed targeted `Get-Printer -Name`/ObjectNotFound suppression was
not adopted without a reproducible provider failure: exact filtering of a successful
enumeration retains wildcard safety and keeps infrastructure errors distinct from
absence. This has an availability tradeoff: unrelated print-provider failures can
block mapping. It is documented rather than rounded away. Windows OUI probing,
full multicast discovery, and transactional recovery remain follow-ups.

Final read-only hardware dry-run matched the Brother capture, constructed the full
profile plan, and exercised actual Windows preflight: elevated=true,
driver_checked=true, driver_present=false. It stopped for the deliberately unset
OEM driver, with confirmed=false and no executed commands. Build, vet, and regression
verification passed after the review fixes; successful physical mapping is still
not claimed. The two Claude review outputs are claims checked against the code/tests,
not substitutes for this evidence.

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

## Real-hardware verification, Step 1: first captured evidence (2026-09-06)

First session run on an actual Windows device (`docs/real-hardware-verification.md`'s handoff
scenario). Environment had neither `git` nor `go` on `PATH`; installed both via `winget`
(`Git.Git`, `GoLang.Go`) with the operator's explicit prior authorization, then verified
`go build`/`go vet`/`go test ./... -count=1` still passed clean before touching anything else.

The operator had a real Brother printer on his LAN but not the exact IP; located it by ping-
sweeping the local `/24`, port-scanning the live hosts for printer-typical ports, and ruling out
the router (`192.168.68.1` resolved to `OPNsense.internal` via PTR) before asking the operator to
confirm the remaining candidate. That candidate (`.243`) turned out to be wrong when checked
against the real device — reported back rather than guessed past, and the operator supplied the
correct IP (`.108`) directly.

**Real captured evidence obtained** (`spoolsmith.exe catalog probe`/`inspect` against `.108`):
`snmp_sys_descr="Brother NC-8300w, Firmware Ver.S  ,MID 84U-F06"`,
`http_title="Brother HL-L2315D series"`, `pjl_id="Brother HL-L2315D series:84U-F06:Ver.1.21"`,
`open_ports=[80,443,631,9100]`, `hostname="brw30c9ab962e73"`. `inspect` correctly failed closed on
first contact (`confidence: 0`) — the real device is an **HL-L2315D**, which was not in the
`brother-hl-l2xxx` family's alias list (only HL-L2325DW/2350DW/2370DW/2370DWXL were present). This
is exactly the class of finding `real-hardware-verification.md` Step 1 asked to surface rather than
force a match on: **the catalog's alias list was incomplete relative to the real product line**,
not a resolver defect. Fixed by adding `"Brother HL-L2315D"`/`"HL-L2315D"` to
`internal/catalog/family.go`'s existing alias list — same pattern as every existing entry, no new
matching logic. Re-verified: `inspect` now resolves the real device to
`family=brother-hl-l2xxx, model="Brother HL-L2315D", confidence=1`.

**Assumed-vs-actual evidence gaps this closes disclosure on, per DR-0005 §6:** the synthetic
Brother fixture assumed `snmp_sys_descr` would contain the printer model name
(`"Brother HL-L2350DW series"`); the real device's SNMP `sysDescr` instead names its network
interface module (`"Brother NC-8300w"`), not the printer — model identity was only recoverable
from HTTP title and PJL ID. The real PJL `INFO ID` response is also a flat colon-separated string
(`"Brother HL-L2315D series:84U-F06:Ver.1.21"`), not the structured `MFG:...;MDL:...;CLS:...;`
format the synthetic fixture assumed (a format `resolve.go`'s `pjlManufacturer()` regex still
expects — it simply finds no match on this device's real format rather than conflicting, so
resolution still worked, but the assumption was wrong and is disclosed here rather than left
implicit).

**Reliability observation, not treated as a defect:** the first-ever probe against the real device
returned much thinner evidence (`open_ports=[443]` only, SNMP success but HTTP/PJL timeouts) than
every subsequent run against the same device seconds later (`open_ports=[80,443,631,9100]`, all
probes succeeding). Re-ran twice more and got the fuller result both times — consistent with cold
first-contact latency (ARP resolution / device network-stack wake) rather than a probe bug, but
flagged here rather than silently discarded since `internal/probe/ports.go`'s 1s-per-port dial
timeout is exactly the kind of margin that would be sensitive to this. Not fixed; noted as a
possible future finding if it recurs.

**Real gap found, not yet fixed:** `internal/probe/oui.go` hard-fails vendor-by-MAC lookup on any
non-Linux `GOOS` (`probeOUI`, line 26) — confirmed directly on this real Windows run
(`"oui": {"success": false, "detail": "ARP cache lookup is only implemented on Linux"}`). Does not
block resolution (OUI is one of several independent, non-load-bearing probes per this repo's own
architecture rule), but it's a real, now-confirmed-on-real-hardware platform gap, left open pending
the operator's direction rather than fixed speculatively in the same round as an unrelated fixture
fix.

Fixture saved as `fixtures/brother-hl-l2315d-captured.json` (`"provenance": "captured"`), added to
`TestResolveKnownFixtures` (`internal/catalog/resolve_test.go`) and
`TestInspectDeterministicGoldenOutput` (`internal/inspect/inspect_test.go`), golden output
regenerated for all four fixtures (the pre-existing `brother-hl-l2350dw-synthetic.json` golden file
also changed, since the family's `Aliases` list — embedded verbatim in `inspect`'s JSON output — now
includes the two new alias entries). Full suite re-run clean: `go build ./...`, `go vet ./...`,
`go test ./... -count=1`.

**This closes DR-0005 §6's "at least one captured, not only synthetic, real-device observation for
at least one claimed family" condition for the Brother family.** HP LaserJet Pro M4xx still has no
captured fixture — that printer wasn't reachable this session. Milestone one's captured-evidence
condition is not fully closed until HP has one too, per the same definition of done.

**Not yet attempted this session:** Step 2 onward of `real-hardware-verification.md` (finding the
real `WindowsDriverName`, staging a vendor driver package, and a real install/uninstall cycle).
Requires Administrator rights this session's shell does not currently have (the Administrators
group token showed "deny only" — UAC has not elevated it) and a decision on how to obtain the real
HP printer's evidence. Both raised to the operator rather than assumed past.
