# Copy workflow — real-hardware validation, 2026-09-15

Validation of the v0.5.0 machine-to-machine copy workflow (`printers`, `copy`, `apply`,
`bundle inspect`, `repoint`) against real Windows and a real printer.

## Environment

| | |
|---|---|
| Machine | `DESKTOP-BBQQE4J`, Windows 11 build 10.0.26200.9457 |
| Account | `kubert`, Administrator (`IsInRole` returned True) |
| Access | OpenSSH, driven from the Linux host |
| Printer | Brother HL-L2315D at 192.168.68.108, reachable from the machine under test |
| Binary | `spoolsmith.exe`, cross-compiled `GOOS=windows GOARCH=amd64`, stamped `v0.5.0-rc3` |
| Starting state | Queue `SpoolSmith VM Brother` → `Brother HL-L2315D series` via `SpoolSmith-192.168.68.108`; driver store package `oem16.inf` (`brohl13a.inf`) |

Only one machine was available. "Source" and "target" are therefore the same machine, with the
target state produced by deleting the driver rather than by using a second PC. That distinction
is called out where it matters.

## Defects found, and fixed

Both were in PowerShell text that the unit suite only ever matched as strings, so neither could
have failed in CI. Both were found on the first real run.

### 1. `$matches` collided with PowerShell's automatic `$Matches`

`exportDriverCommand` counted driver-store matches in a variable named `$matches`. PowerShell
variable names are case-insensitive, and `$Matches` is the automatic hashtable that `-match`
populates — which the same script reads two lines later for its capture groups. `$matches++`
therefore tried to increment a `Hashtable`:

```
The '++' operator works only on numbers. The operand is a 'System.Collections.Hashtable'.
```

The driver export could never have succeeded on any machine. Renamed to `$matchCount`;
`TestExportDriverCommandAvoidsAutomaticVariables` now rejects any assignment to the automatic
name.

### 2. `pnputil` printed into the result stream

With the counter fixed, the export reached `pnputil /export-driver`, which writes its own banner
and progress to the same stream carrying the script's JSON result. Decoding failed with:

```
install: decode driver export: invalid character 'M' looking for beginning of value
```

`pnputil`'s output is now discarded, and `decodeDriverExport` takes the last JSON object in the
stream rather than assuming the result is alone there.

## Results

All runs below used the rebuilt binary with both fixes.

| Step | Command | Result |
|---|---|---|
| List queues | `printers` | Both queues listed; `Microsoft Print to PDF` correctly marked `!` with the `PORTPROMPT:` reason |
| Copy with driver | `copy "SpoolSmith VM Brother" office.ssb --include-driver --note "verification run"` | Exported 115 files / 25,906,495 bytes from `oem16.inf`; wrote a 9,516,053-byte bundle |
| Re-read | `bundle inspect office.ssb` | Manifest read; all payload hashes matched |
| Preview | `apply office.ssb --dry-run` | Probed 192.168.68.108 live; reported "live model evidence matches the capture"; emitted fingerprint `536445b1…` |
| Apply, driver present | `apply office.ssb --plan-hash 536445b1…` | Fingerprint accepted; `Unchanged driver` / `Unchanged port` / `Unchanged printer` — idempotent |
| Wrong fingerprint | `apply office.ssb --plan-hash 0000…` | Refused, naming both fingerprints. No mutation |
| Repoint preview | `repoint "SpoolSmith VM Brother" 192.168.68.150 --dry-run` | Correct plan: queue and driver kept, port moved, old port explicitly retained |

### Driver-staging path, proved directly

The one path with no hardware behind it was applying a bundle to a machine that does *not* have
the driver. That state was manufactured:

1. `uninstall "SpoolSmith VM Brother" --purge-driver --yes` — removed the queue. It reported
   `Retained shared port` and `Retained shared driver` (see the finding below).
2. `Remove-PrinterDriver` then failed with *"The specified driver is in use by one or more
   printers"* even though `Get-Printer` no longer listed any user of it.
3. `Restart-Service Spooler -Force`, then `Remove-PrinterDriver` succeeded, and
   `pnputil /delete-driver oem16.inf /uninstall` deleted the driver-store package.
4. Verified genuinely absent: 0 registered `L2315D` drivers, 0 `brohl13a` driver-store entries.

Then `apply office.ssb --yes`:

```
Catalog BROHL13A.CAT signed by: CN=Microsoft Windows Hardware Compatibility Publisher,
  OU=MOPR, O=Microsoft Corporation, L=Redmond, S=Washington, C=US
Adding driver package:  brohl13a.inf
Driver package added successfully.
Published Name:         oem16.inf
Registered driver from bundle
Created printer
```

Final state: queue `SpoolSmith VM Brother` present via `SpoolSmith-192.168.68.108`, 1 registered
`L2315D` driver, 1 `brohl13a` driver-store entry.

This exercised the whole payload path: hash-verified extraction, catalog signature check,
`pnputil /add-driver`, `Add-PrinterDriver`, and queue creation. The catalog signature check is
the documented trust anchor for a bundle payload, and it demonstrably ran and passed.

The port step reported `Unchanged port`, because the earlier uninstall had retained the port. Port
*creation* from a bundle is therefore not covered by this run; it is covered by the pre-existing
add/install validation.

## Finding not fixed: `--purge-driver` can retain an unused driver

Windows removes a print queue asynchronously. SpoolSmith's driver-removal guard re-reads
`Get-Printer` to check whether anything still uses the driver, and on this run that read still
returned the queue that had just been deleted — so the guard correctly concluded the driver was
in use and kept it. The subsequent `printers` listing also still showed the removed queue; a
later invocation showed it gone.

The guard is not wrong to be conservative, and nothing unsafe happened: the failure mode is a
driver being retained, never one being removed while in use. But `--purge-driver` does not
reliably do what its name says, and the operator is not told why.

Reproducing the removal by hand afterwards needs a spooler restart; until then Windows reports
the driver as in use and both `Remove-PrinterDriver` and `pnputil /delete-driver` refuse.

Left unfixed in v0.5.0 and documented in the README's limitations. A fix would need to wait for
the spooler to settle before the in-use check, and report honestly when it retains a driver.

## Not covered

- A genuine second machine. The target state was manufactured on the source machine.
- A live `repoint` mutation — only its preview was run, to avoid moving a working queue to an
  address with no printer on it.
- A physical test print through a bundle-staged driver.
- Anything on Windows PowerShell 5.1; this machine used PowerShell as shipped with Windows 11.
