# Real-hardware verification runbook

**Update, 2026-09-06:** Brother HL-L2315D discovery, signed driver registration,
SpoolSmith queue creation and repeat-add no-op behavior are verified. The operator
sent a test print to Brother Home and observed it print successfully. HP verification
and live removal are still pending. The original preparation runbook below predates
the working profile path; see `daily-use-spec.md` and `dev-process.md` for current use.

**Update, 2026-09-12:** Two things changed here, and the second is why "live removal
pending" was a more serious entry than it looked.

The Brother HL-L2315D driver name is now in the catalog, so the automatic path
resolves a complete plan against the real printer — confirmed live: `install
192.168.68.108 --dry-run` reports `"resolution": "automatic"` with
`Brother HL-L2315D series`, then stops at the elevation gate. HP remains unverified
and stays fail-closed, as does every other Brother model in the family: the name is
bound per model, never per family, because Brother names drivers per model.

**Removal was not merely unverified, it was broken.** `LookupPrinter` generated
PowerShell emitting `PrinterName`/`PortName`/`DriverName`, while
`PrinterConfiguration` carries `printer_name`/`port_name`/`driver_name` tags. Go's
decoder accepts a case-insensitive field match but does not bridge an underscore, so
every real lookup decoded to an empty configuration **with a nil error** — removal
then failed with either "printer name and port name are required" or the baffling
`installed queue differs from profile (port "", driver "")`. No test caught it
because no test put real PowerShell output through the real decoder, and the
elevation check ran first and masked it on every unelevated attempt.

Both are fixed. Removal now reads the live queue correctly, and the preflight moved
to after the plan is shown so a removal is reviewable *before* you grant admin —
the gate is unchanged, since `Uninstall` re-checks elevation at the mutation
boundary.

**Update, 2026-09-12 (later): the elevated run below has now been done.** Live removal,
both port-retention branches, re-add, and — with the Driver Store genuinely emptied of
the package — the first real run of the `verified-local-archive-if-missing` staging
path. See "Outcome" under Step 4b. The remaining Brother gap is a physical print after
that staging cycle; HP is untouched for want of hardware.

This is the one thing standing between "detection works, install is inert by design" and a
genuinely functioning end-to-end install for the two authorized families (HP LaserJet Pro M4xx,
Brother HL-L2xxx). Nothing here is a code problem — `internal/install` is fully implemented,
reviewed, and independently verified. What's missing is two real strings and a real device to
point at.

Written ahead of time so execution is fast once a Windows VM + real printer access exists — see
"Open questions for the handoff" at the bottom before starting.

## Prerequisites

- A Windows VM (10/11) with network reachability to both printers (same LAN/VLAN, or routed —
  SNMP/HTTP/PJL all need direct IP reachability, no NAT games).
- Administrator rights on the VM (required for every step past evidence capture — SpoolSmith
  itself checks and fails closed if this isn't true, but the driver-staging steps below need it
  too).
- `spoolsmith.exe` from the [v0.1.0 release](https://github.com/spilloid/spoolsmith/releases/tag/v0.1.0),
  or a fresh `go build` from `main` once `WindowsDriverName` is populated (see Step 2).
- The two real printers, powered on, on the network, with known IPs.

## Step 1 — Capture real evidence (closes the milestone-one "captured fixture" gap too)

Before touching drivers at all, run detection against each real device and save the raw evidence —
this is genuinely useful on its own and closes a disclosed gap that's been open since milestone
one (no *captured*, only *synthetic*, fixtures existed until now):

```powershell
.\spoolsmith.exe catalog probe <hp-ip>   > hp-captured-probe.json
.\spoolsmith.exe inspect <hp-ip>         > hp-captured-inspect.json
.\spoolsmith.exe catalog probe <brother-ip> > brother-captured-probe.json
.\spoolsmith.exe inspect <brother-ip>       > brother-captured-inspect.json
```

Sanity-check `inspect`'s output: does it actually resolve to `hp-laserjet-m4xx` /
`brother-hl-l2xxx` with `confidence: 1` and empty `uncertain`? If either doesn't resolve cleanly,
**stop here and report the raw evidence back** — that's a real finding about the catalog's alias
list or the family's real-world SNMP/PJL/HTTP strings needing a fix, more valuable to find now
than after driver work.

Save `*-captured-probe.json`'s evidence block as a new fixture file under `fixtures/`, with
`"provenance": "captured"` and no `provenance_note` needed (that field is only required for
`"synthetic"`) — this is the actual fixture the milestone-one definition of done has been waiting
on.

## Step 2 — Find the real Windows driver name for each family

This is the actual blocker. `Add-Printer -DriverName` and `Get-PrinterDriver -Name` both need the
*exact* string Windows registers a driver under, and nobody has confirmed either one yet.

**Preferred path — let Windows do the resolution itself:**

```powershell
# Trigger Windows' own driver resolution via its normal add-printer flow.
Add-PrinterPort -Name "verify-hp" -PrinterHostAddress <hp-ip>
# Then use Settings > Printers & Scanners > Add device, or:
Add-Printer -Name "verify-hp" -PortName "verify-hp" -DriverName "<whatever Windows suggests>"
```

If Windows resolves it automatically via Windows Update, the driver is now in the store — capture
its exact name:

```powershell
Get-PrinterDriver | Select-Object Name | Format-List
```

**Fallback path if Windows doesn't have an inbox match** — stage the real vendor package once,
by hand, then check the same way:

- HP: download the **HP Universal Print Driver (PCL 6)** from HP's support site for the
  LaserJet Pro M404/M405 series, run its installer once, then `Get-PrinterDriver`.
- Brother: download the **Full Driver & Software Package** for the specific connected model
  (e.g. HL-L2350DW) from Brother's support site, run it once, then `Get-PrinterDriver`.

Either way, **copy the exact string** — case, spacing, everything — into a note. This is the value
that goes into `internal/catalog/driver.go`.

Clean up the verification printer/port afterward (`Remove-Printer -Name "verify-hp"`,
`Remove-PrinterPort -Name "verify-hp"`) so it doesn't interfere with SpoolSmith's own install run
in Step 4.

## Step 3 — Populate `WindowsDriverName` and rebuild

Once both real names are known, this is a two-line change in
[`internal/catalog/driver.go`](../internal/catalog/driver.go):

```go
"hp-laserjet-m4xx": {
    FamilyID:          "hp-laserjet-m4xx",
    Name:              "HP Universal Print Driver for Windows PCL 6",
    WindowsDriverName: "<exact string from Get-PrinterDriver>",
    ...
},
"brother-hl-l2xxx": {
    FamilyID:          "brother-hl-l2xxx",
    Name:              "Brother model-specific Full Driver & Software Package",
    WindowsDriverName: "<exact string from Get-PrinterDriver>",
    ...
},
```

Report the two strings back and this gets made and committed same-session, with its own
independent verification pass (build/vet/test) before anything else proceeds — same discipline as
every other change in this repo.

## Step 4 — Dry run, then the real thing, for both families

```powershell
.\spoolsmith.exe install <hp-ip> --dry-run
# Review the plan output carefully -- driver name, port, the literal commands.
.\spoolsmith.exe install <hp-ip>
# Confirm when prompted. This is the actual first real mutation this product has ever performed.
```

**After a real install, verify it actually worked** — not just that SpoolSmith reported success:

- `Get-Printer` shows the new printer.
- The printer's port actually points at the right IP (`Get-PrinterPort`).
- **Print an actual test page.** This is the only step in this whole runbook that proves the
  driver is correct, not just that Windows accepted the name — a wrong-but-similarly-named driver
  can install cleanly and still produce garbage output or nothing at all.

Then uninstall and confirm clean removal:

```powershell
.\spoolsmith.exe uninstall "<printer name inspect reported>"
# Confirm when prompted. Verify with Get-Printer / Get-PrinterPort that both are gone.
```

Repeat the full install → verify → print → uninstall cycle for Brother.

## Step 4b — Live removal (2026-09-12: written pending, executed the same day)

Removal is the reversibility half of D-0040's trust model and it has never executed
against real hardware. The decode bug above is fixed and the plan is now verified
correct against the operator's live queue, but a fix confirmed by a preview is not a
fix confirmed by a removal.

Run the preview first, unelevated — this now works and mutates nothing:

```powershell
.\spoolsmith.exe remove --profile profiles\brother-home.json --dry-run
```

Expect the queue name, `SpoolSmith-<ip>`, and the exact driver name to be populated
from live Windows inventory, then `administrator privileges are required`. Empty
port/driver values mean the decode bug is back; stop and fix it rather than granting
admin.

Then, in an **elevated** shell, run the removal itself and confirm each claim:

```powershell
.\spoolsmith.exe remove --profile profiles\brother-home.json
# Review the plan, confirm once.
Get-Printer      | Where-Object Name -eq 'Brother Home'              # expect nothing
Get-PrinterPort  | Where-Object Name -eq 'SpoolSmith-192.168.68.108' # expect nothing
Get-PrinterDriver | Where-Object Name -eq 'Brother HL-L2315D series' # expect STILL PRESENT
```

The driver must survive: removal without `--purge-driver` retains it, and the
uninstall commands additionally retain any port still referenced by another queue
and any port not named `SpoolSmith-`. Verify the retention branches too, ideally by
pointing a second queue at the same port before removing the first.

Then prove the cycle is closed by putting it back and printing again:

```powershell
.\spoolsmith.exe add --profile profiles\brother-home.json
```

This is safe to attempt even though it removes a working printer: the signed package
stays staged in the Driver Store as `oem15.inf` after the queue goes away, so re-adding
needs neither the vendor archive nor a download. Confirm with a real test print, not
just `Get-Printer` — the same reasoning as Step 4.

Record the outcome in `dev-process.md` including anything surprising, then tick the
uninstall box below.

### Outcome — run elevated 2026-09-12

Done, including the retention branches, against the operator's live Brother HL-L2315D.
Full narrative in `dev-process.md`; the claims this runbook asked to confirm:

| Claim under test | Result |
| --- | --- |
| Preview populates port/driver from live inventory (decode fix holds) | confirmed — real values, never `""` |
| Removal deletes the queue | `Removed printer`, `Get-Printer` empty |
| Port retained while a second queue uses it | `Retained shared port`, port survived |
| Port deleted once unused | `Removed unused SpoolSmith port`, port gone |
| Driver survives removal without `--purge-driver` | survived both removals |
| Re-add needs neither archive nor download | `Unchanged driver` + `Created port`/`Created printer` |

**The driver-absent staging path was also exercised for real**, which nothing before
this had done — every prior run took the `Unchanged driver` shortcut because the
package was staged by hand on 2026-09-06. `Remove-PrinterDriver` plus `pnputil
/delete-driver oem15.inf /uninstall` left the machine genuinely without the driver,
and `add --profile` then verified the hash, verified Brother's Authenticode signature,
extracted, verified the CAT against the Microsoft WHCP signer, ran `pnputil
/add-driver` (`Published Name: oem15.inf`) and registered the driver — reporting
`Registered driver`. Final state is identical to the starting state, same DriverStore
directory `brohl13a.inf_amd64_e477ef8d79b8572c`.

**Not confirmed by this run:** a physical test print after the staging cycle. The queue
reports `PrinterStatus: Normal` and the package is the same one that printed on
2026-09-06, but per Step 4's own rule only paper proves a driver.

## Step 5 — Negative-path checks (quick, worth doing once)

- Run `spoolsmith install <ip>` from a **non-elevated** PowerShell window — confirm it fails
  closed with the elevation error. Note it shows the plan *first* and then refuses: that is
  deliberate, so an operator can read what would happen before granting rights. Confirmed live
  2026-09-12 (exit 4, no mutation). `remove` behaves the same way as of the same date. What must
  never appear unelevated is an executed command, not a printed plan.
- Temporarily rename/remove the staged driver and re-run `install --dry-run` — confirm it reports
  the driver-not-present guidance rather than a generic error. Confirmed 2026-09-12 without
  touching the Driver Store, by pointing a scratch dry-run profile at a driver name Windows does
  not have (`Brother HL-L2340D series`, strategy `existing-windows-driver`): the plan prints
  first, then `driver not found — install it via Windows Update or run the vendor package
  manually first, then retry`, exit 4, nothing mutated. Prefer this form — it needs no elevation
  and cannot leave the machine without a working driver.

Note that exit 4 is `ExitPreflight`, shared by *both* negative paths above — elevation refusal and
driver-absent are the same class of fail-closed, distinguished by message, not by code. Don't read
a bare "exit 4" in a log as necessarily meaning the elevation gate.

## Definition of done for this runbook

- [ ] Real captured evidence saved for both families as genuine `fixtures/*.json` (provenance:
      captured), committed. Brother done 2026-09-06
      (`fixtures/brother-hl-l2315d-captured.json`); HP outstanding for want of hardware.
- [ ] Both `WindowsDriverName` values confirmed against real `Get-PrinterDriver` output and
      committed. Brother HL-L2315D done 2026-09-12 (`Brother HL-L2315D series`, bound per model
      in `internal/catalog/driver.go`); HP outstanding. Sibling Brother models in the same family
      are deliberately still unbound — they need their own hardware confirmation, not an
      inherited name.
- [ ] A real install → verified-by-printing → uninstall cycle completed for **both** HP and
      Brother, on the actual VM, against the actual printers. Brother's mutation half is done
      2026-09-12 — install, uninstall (both port branches), and driver staging from the signed
      archive all executed live and verified against `Get-Printer`/`Get-PrinterPort`/`pnputil`
      (Step 4b outcome). The **printing** half is outstanding: the last physical print was
      2026-09-06, before the staging cycle. HP outstanding entirely.
- [x] Both negative-path checks (non-elevated, driver-absent) confirmed still fail closed —
      elevation 2026-09-12 (exit 4, plan shown, no mutation), driver-absent 2026-09-12 (exit 4,
      explicit guidance). See Step 5.
- [x] Everything above logged honestly in `docs/dev-process.md`, including anything that didn't
      go as expected — a clean run on the first try for privileged Windows mutation code would be
      a little suspicious, not a reason to skip writing down what actually happened. The
      2026-09-12 elevated entry records the `tar.exe` PATH defect that first presented as a
      false test failure.

## Open questions for the handoff

Noted here rather than guessed at:

1. **How will this session actually reach the VM?** Direct shell access (SSH/WinRM) would let me
   run these steps myself and iterate fast; if it's air-gapped or access-restricted, the pattern
   is you running the commands above and pasting output back, which still works fine but is
   slower per round-trip. Either is fine — just tell me which when we hand off.
2. **Are the two printers real physical units on your network, or something emulated/virtual?**
   Changes nothing about the steps above, but matters for interpreting an unexpected result (a
   virtual/emulated printer might not behave identically to a real one for SNMP/PJL quirks).
3. **Network path from VM to printers** — same LAN is simplest; if there's a VPN/routing hop
   involved, worth confirming SNMP (UDP 161) and PJL (TCP 9100) actually traverse it before
   assuming a detection failure is SpoolSmith's fault.
