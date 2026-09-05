# Real-hardware verification runbook — the actual remaining work to a working v0.1.0

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

## Step 5 — Negative-path checks (quick, worth doing once)

- Run `spoolsmith install <ip>` from a **non-elevated** PowerShell window — confirm it fails
  closed with the elevation error, before showing any plan.
- Temporarily rename/remove the staged driver and re-run `install --dry-run` — confirm it reports
  the driver-not-present guidance rather than a generic error.

## Definition of done for this runbook

- [ ] Real captured evidence saved for both families as genuine `fixtures/*.json` (provenance:
      captured), committed.
- [ ] Both `WindowsDriverName` values confirmed against real `Get-PrinterDriver` output and
      committed.
- [ ] A real install → verified-by-printing → uninstall cycle completed for **both** HP and
      Brother, on the actual VM, against the actual printers.
- [ ] Both negative-path checks (non-elevated, driver-absent) confirmed still fail closed.
- [ ] Everything above logged honestly in `docs/dev-process.md`, including anything that didn't
      go as expected — a clean run on the first try for privileged Windows mutation code would be
      a little suspicious, not a reason to skip writing down what actually happened.

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
