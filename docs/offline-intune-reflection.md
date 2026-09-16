# Offline and Intune implementation reflection

Reviewed 2026-09-14 against implementation commit `20ab179`, before connecting to
the planned Windows 11 VM. This is the implementation author's follow-up review,
not an independent review or a record of Windows/tenant validation.

## What changed in our understanding

Offline provisioning is a useful separation between a validated configuration and
a currently reachable device. The code now makes that separation explicit: saved
profile validation remains mandatory, live identity checks remain the default, and
offline success needs a fresh local Windows inventory check. Captured identity
still does not authenticate whatever device later occupies an IP address.

The larger part of issue #6 is lifecycle ownership. Generating an install command
is straightforward; safely deciding what a retry, update or uninstall may affect
requires durable state, revision rules, conflict handling and protection of shared
resources. The implementation therefore retains removal inputs before mutation and
keeps both pending and completed revision information. Those decisions need native
failure-path tests at least as much as the initial happy path does.

The first implementation report should be read as **code ready for pilot**, not
"both issues finished." Roughly 1,950 added lines, including tests and docs, landed
in one commit. The breadth increases review burden. Passing Linux tests and a
Windows cross-build established useful properties, but left most OS boundaries
unexecuted. A staged Windows pilot is the next verification step, not ceremony.

## Evidence already obtained

- Go build/vet, the full suite under Go 1.24.0 and 1.27.1, Windows amd64 cross-build
  and vet, and race checks for the install/CLI packages passed.
- PowerShell 7.6.6 on Linux executed generated detection and lifecycle scripts.
  Tests exercised mismatches, repeated application, ownership conflicts,
  same-revision changes, interrupted-update retry, downgrade refusal, removal
  after deleting app sources, and native exit/JSON capture.
- The lifecycle test exposed a real `File.Replace` argument problem, which was
  fixed. Review also prompted explicit UTF-8 handling for Windows PowerShell.
- Native Windows inventory, ACL/privilege behavior and installation were substituted
  in those tests. The GUI was compiled but not exercised in a desktop session.
  No Intune tenant or physical print was involved.

This evidence is strongest for orchestration and script control flow. It is weaker
for the boundaries that actually grant access, stage a driver, or change a queue.
The earlier repository incident where removal silently decoded empty names from
PowerShell JSON is a direct reason to verify those boundaries end-to-end now; see
[the recorded Windows findings](real-hardware-verification.md).

## Concrete findings and questions for the pilot

| ID | Finding from source review | Consequence / next check |
| --- | --- | --- |
| R1 | `install.ps1` validates payloads and initializes protected state before assigning its lifecycle-log path. | Early hash, platform, ACL or root failures have stderr but no deployment lifecycle log. Capture stderr in the VM harness. Revisit the diagnostic contract before calling all failure paths durably logged. |
| R2 | Port creation precedes queue creation. When the queue is absent, `RunUninstall` returns already-absent before port lookup/removal. | Failure between those steps can leave an unused managed port, and the generated removal script can clear the deployment claim afterwards. Reproduce on the VM. A future fix needs recorded port ownership and shared-use checks, not broad prefix-based deletion. |
| R3 | `Is-Matching` uses full compliance for update/removal guards, including registered-driver presence; `Assert-Payload` also checks the retained archive for removal. | Missing registration or a damaged retained payload can prevent cleanup even when the queue name still matches. Record the behavior and decide whether a separately validated removal path should require fewer provisioning inputs. Do not silently loosen the guard. |
| R4 | Each CLI invocation has a ten-minute timeout; a lifecycle script can invoke the CLI several times. The tutorial configures a fifteen-minute app limit. | There is no single internal elapsed-time budget for the entire lifecycle. Test host timeout, descendant termination and pending-state recovery; decide whether an overall deadline is needed. |
| R5 | Compliance logic exists in Go (`CheckStatus`) and generated PowerShell (`detect.ps1`). | Both currently check the intended local fields. Future edits could diverge; run the same Windows mismatch matrix against both surfaces and consider a shared contract fixture. |
| R6 | ACL construction and checks, SYSTEM execution, 32-bit relaunch and Windows PowerShell 5.1 were not exercised by the Linux mocks. | Make these early pilot cases. Do not start by assuming a mock's privilege outcome is how a fresh Windows VM behaves. |
| R7 | Queue renames require explicit retirement/new ID; updates retain old ports and revisions. | This favors avoiding unintended deletion but leaves an administrator cleanup obligation. Test a replacement/move and verify the documentation matches the retained resources. |

R1 and R2 are observed control-flow gaps, not VM-confirmed failures. R3 and R4 are
specific behavior/contract questions. R5–R7 are verification and maintenance risks.
None has been marked resolved merely by writing this document.

## What to retain from this iteration

Keep explicit offline intent, exact local verification, unchanged driver trust
checks, conservative adoption and shared-resource handling. Keep previewed payload
pins and the distinction between local configuration, live identity, physical
printing and tenant delivery. The independent source checkout and backup stash
also preserved unfinished clone/bundle work while main was pulled.

For follow-up changes, prefer small commits tied to one reproduced VM finding,
with a regression test at the failing boundary and the before/after evidence
recorded. Rebuild and hash a new pilot for code changes; do not mix observations
from different executables under one result row. Documentation-only changes do
not require rerunning the entire Go suite or rebuilding an unchanged executable.

## Exit criteria for the next stage

The [Windows runbook](validation/2026-09-14-windows11-pilot.md) orders the work so
native execution and local lifecycle evidence come first. Completing that stage
can establish Windows endpoint behavior. Issue #5 still needs actual disconnected
provisioning and a later print observation. Issue #6 additionally needs the tenant's
Required/Available/removal results. Record missing prerequisites as blocked and
unattempted cases as not-run, rather than converting either into a pass.
