# Windows 11 pilot results

Copy this template into a dated result record after connecting to the VM.
Initial status: **not-run**. Keep raw logs and connection details private; link
sanitized evidence from a tracked report when appropriate.

## Environment and build

| Field | Observed value |
| --- | --- |
| Run date / operator | Pending |
| Source commit / branch | Pending |
| ZIP SHA-256 / CLI SHA-256 / GUI SHA-256 | Pending |
| Windows edition, build and architecture | Pending |
| PowerShell version / process bitness | Pending |
| Session context | SSH / RDP / SYSTEM task / Intune — record each |
| Effective user SID / elevation | Pending |
| Snapshot / restoration point | Pending |
| Registered driver / package ID and hash | Pending |
| Profile source | Real captured/validated profile OR explicitly labeled fixture |
| Printer reachability isolation method | Pending |
| Physical printer / later print observation | Pending |
| Intune enrollment / app revision / assignments | Pending or unavailable |

## Case results

Use only **pass**, **fail**, **blocked**, or **not-run**. A result needs both an
observed outcome and an evidence path; a process start alone is not a pass.

| Case | Context / build hash | Expected result | Observed result and exit code | Evidence path | Status |
| --- | --- | --- | --- | --- | --- |
| Baseline inventory and effective privilege | | Read-only baseline recorded | | | not-run |
| Offline dry-run | | No queue/port mutation | | | not-run |
| Offline first add / repeat / configure | | Local success, no duplicates | | | not-run |
| Default strict mode while unreachable | | Fails without offline fallback | | | not-run |
| Local status mismatch matrix | | Correct absent/driver/address/protocol/port outcomes | | | not-run |
| Supported archive staging / invalid hash/signature | | Valid staging or explicit rejection | | | not-run |
| Missing registered driver | | Clear prerequisite failure | | | not-run |
| No confirmation in unattended context | | Exit 5, no mutation/prompt | | | not-run |
| Fresh protected state under SYSTEM/Admin | | Correct ownership and access in both orders | | | not-run |
| Standard-user write attempts | | Denied for executable/profile/scripts/metadata | | | not-run |
| Reparse/untrusted path refusal | | Stops before use | | | not-run |
| SYSTEM installation / local detection | | Exit 0 plus actual matching inventory | | | not-run |
| Detection negative matrix | | Nonzero exit, no success output | | | not-run |
| Adoption / another deployment's claim | | Exact matching adoption only; conflict refused | | | not-run |
| Revision update / downgrade / rename | | Supported update only | | | not-run |
| Interrupted install/update and retry | | Pending state recoverable; no false detection | | | not-run |
| Port created but queue absent (R2) | | Record residual ownership/cleanup behavior | | | not-run |
| Pre-log failure (R1) | | Rejection captured externally | | | not-run |
| Missing removal inputs / registration (R3) | | Record conservative refusal/recovery | | | not-run |
| Lock contention / whole-operation timeout (R4) | | Correct return code and no lingering mutation | | | not-run |
| PowerShell 5.1 Unicode / 32-bit entry | | Preserved names, x64 execution and exit code | | | not-run |
| Cache-independent removal / repeated removal | | Intended queue gone; removal inputs usable | | | not-run |
| Shared queue/port/driver protection | | Other queue and shared resources remain | | | not-run |
| RDP wizard: validation, preview, edit, cancel, export | | Choices and exported contents agree | | | not-run |
| RDP wizard at minimum size / display scaling | | Readable, usable controls | | | not-run |
| Connectivity restored / physical print | | Operator observes correct physical output | | | not-run |
| Intune Required / standard-user Available | | Real tenant delivery and endpoint result | | | not-run |
| Company Portal uninstall / dependencies | | Observed tenant behavior recorded | | | not-run |
| Explicit Uninstall assignment / retirement | | Queue removed; shared resources preserved | | | not-run |

## Findings and retests

For each failure: describe the trigger, exact observed output, affected resources,
reproduction steps, source finding, fix commit/new artifact hash, regression test,
and native retest evidence. Keep the original failed result instead of overwriting
it with the retest. Record cleanup/snapshot restoration and any remaining resources.

## Closure assessment

- Issue #5: pending — state which acceptance criteria have actual Windows and print evidence.
- Issue #6: pending — distinguish endpoint tests from real Intune/Company Portal results.
- Remaining failures, blocked prerequisites and not-run cases: pending.
