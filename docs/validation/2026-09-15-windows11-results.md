# Windows 11 pilot results — 2026-09-15

Execution record for the runbook in
[`2026-09-14-windows11-pilot.md`](2026-09-14-windows11-pilot.md), covering issues
#5 and #6. Raw stdout/stderr/exit files for every case are under
`dist/vm-evidence/2026-09-15/` on the machine that ran the pilot. That path is
untracked (`/dist/` is gitignored), which is deliberate: the runbook keeps raw
logs and connection details out of the repository. Evidence filenames below are
relative to it.

This record supersedes the template's `not-run` defaults only where an observed
outcome and an evidence path both exist. Cases with no evidence stay `not-run`
even where the surrounding behavior looks healthy.

## Environment and build

| Field | Observed value |
| --- | --- |
| Run date / operator | 2026-09-15, operator at the console; VM driven over SSH and RDP |
| Source commit / branch | `71bbb75` on `codex/offline-intune` (pilot ZIP built from `20ab179`) |
| ZIP SHA-256 | `45cb2c69070b83766414e202c6e9581c2654a5c97a6cd8402fb7f88542a616f7` |
| CLI SHA-256 | `8906a34e6d98af431ca155c962ed1bfa98293b4c8436186319ee31bf5c09c38a` |
| GUI SHA-256 | `3c7408d0104a806e1800ee753640c03932082d3d4a35764c30450679b159d27c` |
| Windows edition, build and architecture | Windows 11 Pro, build 26200, 64-bit |
| PowerShell version / process bitness | Windows PowerShell 5.1.26100.9444, 64-bit process |
| Session context | SSH (elevated, `kubert`), RDP session 2, SYSTEM scheduled task, standard-user batch task |
| Effective user SID / elevation | `S-1-5-21-3235105482-419970887-3181440717-1002`, elevated |
| Snapshot / restoration point | None taken — see [Findings](#findings-and-retests), F-5 |
| Registered driver / package ID and hash | `Brother HL-L2315D series`; archive `brother-y14a-c1-hostm-1110`, SHA-256 `6814E22081074524AB08B687AFB5965B3577AEE1A40D97FC39012BF758B8A0AE`, Authenticode `Valid`, signer `CN="Brother Industries, Ltd."` |
| Profile source | Two: a **real captured profile** for the Brother HL-L2315D at 192.168.68.108 (SNMP + HTTP + hostname, `provenance: captured`), and an **explicitly labeled fixture** at 192.0.2.40/.41 for queue mechanics only |
| Printer reachability isolation method | Outbound firewall rule on the printer address only; management path preserved. First attempt was invalid (F-4) — see below |
| Physical printer / later print observation | Operator confirmed a physical test page printed correctly on the Brother HL-L2315D |
| Intune enrollment / app revision / assignments | **Unavailable** — VM is not enrolled and no tenant was available this session |

## Case results

| Case | Context / build hash | Expected result | Observed result and exit code | Evidence path | Status |
| --- | --- | --- | --- | --- | --- |
| Baseline inventory and effective privilege | SSH elevated, `8906a34e` | Read-only baseline recorded | Baseline captured: one inbox queue, no SpoolSmith ports; elevated 64-bit PS 5.1 | `vm-stage.stdout` | **pass** |
| Offline dry-run | SSH elevated | No queue/port mutation | Exit 0; queue and port counts unchanged after preview | `vm-offline-cases.stdout` | **pass** |
| Offline first add / repeat / configure | SSH elevated | Local success, no duplicates | Exit 0/0/0; exactly one queue and one port afterwards | `vm-offline-cases.stdout` | **pass** |
| Default strict mode while unreachable | SSH elevated | Fails without offline fallback | Exit 3 | `vm-offline-cases.stdout` | **pass** |
| Local status mismatch matrix | SSH elevated, pilot CLI `8906a34e` | Correct absent/driver/address/protocol/port outcomes | All eight cases returned the expected exit code and the expected mismatch reasons — see below | `status-matrix.json`, `status-*.stdout.json` | **pass** |
| Supported archive staging / invalid hash/signature | SSH elevated, real Brother archive | Valid staging or explicit rejection | Staging exit 0 with valid signature; tampered archive rejected, exit 1 | `vm-brother-offline.stdout` | **pass** |
| Missing registered driver | SSH elevated | Clear prerequisite failure | Exit 4 | `vm-brother-offline.stdout` | **pass** |
| No confirmation in unattended context | SSH elevated | Exit 5, no mutation/prompt | Exit 5 | `vm-offline-cases.stdout` | **pass** |
| Fresh protected state under SYSTEM/Admin | SYSTEM task + elevated admin | Correct ownership and access in both orders | State created SYSTEM+Administrators only, inherited; both orders correct | `vm-system.stdout` | **pass** |
| Standard-user write attempts | Standard-user batch task, real unprivileged token | Denied for executable/profile/scripts/metadata | All denied: read, list, create, append to `runtime.ps1`, delete claim, and `Remove-Printer` on the managed queue. Positive control (write to own dir) succeeded, so the denials are real | `vm-acl-batch.stdout`, `standard-user-acl.json` | **pass** |
| Reparse/untrusted path refusal | Elevated | Stops before use | Writable state rejected, exit 1; restored ACL accepted, exit 0 | `vm-system.stdout` | **pass** |
| SYSTEM installation / local detection | SYSTEM task | Exit 0 plus actual matching inventory | Install and detect both exit 0 with matching queue/port inventory | `vm-intune-admin.stdout` | **pass** |
| Detection negative matrix | Elevated | Nonzero exit, no success output | Old revision exit 1; pending-only state not detected, exit 1 | `vm-revision.stdout` | **pass** |
| Adoption / another deployment's claim | — | Exact matching adoption only; conflict refused | Not exercised | — | not-run |
| Revision update / downgrade / rename | Elevated | Supported update only | R1→R2 update exit 0 and detected; downgrade refused, exit 1 | `vm-revision.stdout` | **pass** |
| Interrupted install/update and retry | — | Pending state recoverable; no false detection | Not exercised | — | not-run |
| Port created but queue absent (R2) | — | Record residual ownership/cleanup behavior | Not exercised | — | not-run |
| Pre-log failure (R1) | — | Rejection captured externally | Not exercised | — | not-run |
| Missing removal inputs / registration (R3) | — | Record conservative refusal/recovery | Not exercised | — | not-run |
| Lock contention / whole-operation timeout (R4) | — | Correct return code and no lingering mutation | Not exercised | — | not-run |
| PowerShell 5.1 Unicode / 32-bit entry | Elevated, 32-bit PS entrypoint | Preserved names, x64 execution and exit code | 32-bit entry relaunched x64 and updated to R2, exit 0; exit codes preserved after F-1 fix | `vm-revision.stdout`, `vm-exitcode-fixed.stdout` | **pass** |
| Cache-independent removal / repeated removal | Elevated; both source bundles moved out of their cache paths first | Intended queue gone; removal inputs usable | Removal ran from ProgramData alone, exit 0; repeat run exit 0 and idempotent; claim removed | `vm-uninstall-cacheless.stdout` | **pass** |
| Shared queue/port/driver protection | Same run | Other queue and shared resources remain | Second queue on the *same port*, the unrelated Brother queue and port, and the shared driver all survived removal | `vm-uninstall-cacheless.stdout` | **pass** |
| GUI launch and widget render (RDP session) | RDP session 2, rebuilt GUI with embedded manifest | Window appears with usable controls | Window rendered, 50 automation elements, all five tabs present; clean stderr; stable across repeat launches | `gui-embedded-no-sidecar.png`, `gui-embed.json` | **pass** |
| RDP wizard: validation, preview, edit, cancel, export | — | Choices and exported contents agree | **Not exercised** — only launch and widget render were confirmed; no wizard interaction was driven | — | not-run |
| RDP wizard at minimum size / display scaling | — | Readable, usable controls | Not exercised | — | not-run |
| Connectivity restored / physical print | Brother HL-L2315D, real driver | Operator observes correct physical output | Job submitted, `ReturnValue: 0`; operator confirmed the page printed correctly | `vm-print.stdout` + operator confirmation | **pass** |
| Intune Required / standard-user Available | — | Real tenant delivery and endpoint result | No tenant available | — | **blocked** |
| Company Portal uninstall / dependencies | — | Observed tenant behavior recorded | No tenant available | — | **blocked** |
| Explicit Uninstall assignment / retirement | — | Queue removed; shared resources preserved | No tenant available | — | **blocked** |

### Local `status` mismatch matrix

`CheckStatus` makes seven independent checks and `status` returns 0 when compliant
and `ExitUnresolved` (3) otherwise. Each check was driven by building the exact
Windows queue/port state it discriminates on, against a labeled fixture profile
(`192.0.2.40`, `Microsoft Print To PDF`). The unrelated Brother queue was
asserted to survive the whole matrix.

| Case | Queue/port state built | Exit | Mismatch reasons returned |
| --- | --- | --- | --- |
| `absent` | nothing installed | 3 | all seven reasons |
| `compliant` | exactly what the profile describes | 0 | none |
| `driver-differs` | right port, `Brother HL-L2315D series` driver | 3 | `driver differs` |
| `port-name-differs` | right address/protocol/port, port not named `SpoolSmith-192.0.2.40` | 3 | `managed port is absent or differs` |
| `address-differs` | managed port name, host address `192.0.2.99` | 3 | `target address differs` |
| `protocol-not-raw` | LPR port instead of RAW | 3 | `port protocol is not RAW`, `port number is not 9100` |
| `port-number-differs` | RAW on port 9101 | 3 | `port number is not 9100` |
| `case-insensitive-name` | queue named `spoolsmith status MATRIX` | 0 | none |

Two results worth stating rather than glossing. `protocol-not-raw` returns *two*
reasons, not one: a Windows LPR port carries no 9100 port number, so the port-number
check fires alongside the protocol check. That is correct behavior and not
double-reporting — the two checks are independent — but a caller matching on a
single reason string would miss it. And `case-insensitive-name` confirms the
`EqualFold` name comparison is deliberate: a queue whose name differs only in case
is treated as compliant, so queue names are matched case-insensitively end to end.

## Findings and retests

### F-1 — Native exit codes lost under Windows PowerShell 5.1

**Trigger.** Launching the CLI with `Start-Process -PassThru` and reading
`$p.ExitCode` after `WaitForExit()`. **Observed.** `ExitCode` was `$null`, so a
failing command could be read as success. Reproduced in the *generated* runtime,
not only in the test harness, which is what made it a product defect rather than
a harness artifact. **Affected.** `internal/intune/templates/runtime.ps1` and
every lifecycle script built from it. **Fix.** Cache the process handle before
waiting so .NET keeps the exit code, and refuse a missing exit code instead of
treating it as zero — commit `92e7f5d`. **Retest.** `vm-exitcode-fixed.stdout`
shows real codes preserved in both the harness and the generated runtime.

### F-2 — Standalone detection accepted writable deployment state

**Trigger.** Deployment state whose ACL permits non-administrators, or that is
reached through a reparse point. **Observed.** Detection reported success.
**Fix.** Detection now validates ownership and refuses untrusted permissions or
reparse points — commit `71bbb75`. **Retest.** `vm-system.stdout`:
writable state rejected with exit 1, restored ACL accepted with exit 0.

### F-3 — Side-car GUI manifest fails permanently if it arrives after first launch

**Trigger.** The desktop app shipped `spoolsmith-gui.exe.manifest` as a side-car
file next to the executable. **Observed.** The GUI exited with code 1 and
`InitCommonControlsEx failed` on stderr, painting no window at all. Because the
app is linked `-H windowsgui`, a user sees *nothing* — no window, no error, no
console.

Isolated with a four-arm test at fresh paths (`gui-iso.json`):

| Arm | Side-car present at first launch | Result |
| --- | --- | --- |
| Renamed exe + matching renamed side-car | yes | **works** — window, 50 controls |
| Exe alone, no side-car | no | fails, exit 1 |
| Exe alone, no side-car | no | fails, exit 1 |
| Same path as above, side-car added afterwards | added after | **still fails, exit 1** |

Renaming is harmless. What matters is ordering: Windows caches an executable's
activation context per path, so an executable launched even once without its
manifest stays broken at that path, and dropping the manifest in afterwards does
not repair it. Recovery requires replacing the executable or changing its
timestamp — not something a user would ever guess. Any packaging step that
copies the exe before the manifest, or omits it, ships a silently dead GUI.

**Fix.** Embed the manifest in the binary as an `RT_MANIFEST` resource and stop
shipping the side-car. `internal/winres` builds the resource object;
`internal/winres/mkwinres` generates it; `cmd/spoolsmith-gui/rsrc_windows_amd64.syso`
is committed and regenerated with `go generate ./cmd/spoolsmith-gui`. The
manifest file remains the source of truth, and
`TestGeneratedResourceObjectMatchesManifest` fails if the committed object drifts
from it. No third-party tooling or network access is needed to build a release.

**Retest.** A rebuilt GUI (SHA-256 `44c4211039f46ccc31e17fffc4669022c2d09af179c4da637f7fd139b033378c`)
placed alone in a fresh directory with no `.manifest` file anywhere launched
correctly on both runs — window rendered, 50 automation elements, all five tabs
(`Find a printer`, `Add printer`, `Saved printers`, `Review and apply`, `Tools`),
empty stderr (`gui-embed.json`, screenshot `gui-embedded-no-sidecar.png`).

### F-4 — First Brother isolation run did not prove isolation

**Trigger.** The offline test blocked the printer with a firewall rule, but all
three Windows Firewall profiles were disabled on this VM, so the rule was inert.
**Observed.** Strict mode returned 0 where 3 was expected
(`brother-strict-blocked` in `vm-brother-offline.stdout`) because the printer was
still reachable. The driver-staging and queue results from that run remain valid;
the *offline* claim did not. **Retest.** Isolation re-established and verified by
a direct TCP 9100 probe before testing: strict mode exit 3, explicit offline mode
exit 0, firewall state restored (`vm-isolate-brother.stdout`). The original
failed result is kept above rather than overwritten.

### Observation — removal deliberately retains payload

`uninstall.ps1` removes the queue and the deployment claim and then logs
"retained driver, shared ports, diagnostic files and previous revisions". After
removal the state directory still holds `runtime.ps1`, `uninstall.ps1`, every log,
and **each revision's full payload including its own copy of `spoolsmith.exe`**.
That is the template's stated intent, not a defect, and it is what makes
cache-independent removal possible. It does mean an Intune "uninstall" leaves the
payload on disk, and that per-revision copies accumulate without bound. Worth a
retention policy before many revisions ship; not a blocker.

### F-5 — No VM snapshot was taken

The runbook calls for a snapshot before driver and queue mutations. None was
taken, so driver-store changes on this VM are not cleanly reversible. Cleanup was
done by targeted removal instead, and the post-run inventory is recorded in
`vm-uninstall-cacheless.stdout`. Take the snapshot before the tenant session.

## Closure assessment

- **Issue #5 (offline provisioning):** the endpoint half has real Windows
  evidence — preview, add, repeat, configure, strict-vs-offline behavior under
  *verified* isolation, unattended refusal, invalid-argument handling, a real
  captured Brother profile, signed-archive staging with a tampered-archive
  rejection, the full local `status` mismatch matrix across all seven checks, and
  an operator-confirmed physical page. No blocking failures and no not-run cases
  remain in #5's own scope.
- **Issue #6 (Intune lifecycle):** every part that a standalone endpoint can
  prove now passes — SYSTEM install and detection, the detection negative matrix,
  revision update and downgrade refusal, the 32-bit entrypoint, protected-state
  enforcement against a real standard-user token, cache-independent and repeated
  removal, and preservation of shared queues, ports and drivers. **Nothing about
  real tenant delivery is verified**: Required/Available assignment, Company
  Portal behavior, and Uninstall-assignment retirement are all blocked on a
  tenant and remain untested.
- **Remaining failures, blocked prerequisites and not-run cases:** three Intune
  tenant cases blocked; recovery cases R1–R4, adoption/claim-conflict, interrupted
  install retry, and the GUI wizard interaction and scaling cases are not-run.
  F-1 through F-4 are fixed and retested; F-5 is a process gap to close before the
  tenant session.

**#5 is closeable on this record.** #6 is not: it needs the tenant session, and no
amount of endpoint evidence substitutes for real Required/Available delivery,
Company Portal behavior, and Uninstall-assignment retirement.
