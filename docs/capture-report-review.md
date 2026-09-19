# Review — "Reconstructing Windows Printer Deployments Without Vendor Installers"

Read-only review of an external research report proposing a capture-and-redeploy direction for
SpoolSmith: working queue → resolve the exact Driver Store package → export it intact → portable
profile → `.intunewin` → Intune deployment. The report was reviewed against this repo's code,
`CLAUDE.md`, `docs/daily-use-spec.md`, and `corporate-strategy/state/products/SpoolSmith.md`, and
its central technical claims were tested live on the operator's own Windows 11 Pro 26200 machine
against the real `Brother Home` queue that D-0041 recorded.

Verdict in one line: **the capture half is sound, cheaper to build than the report thinks, and
already inside current authorization. The deployment half collides head-on with D-0040's
confirmation gate and cannot be built from this report.**

Everything below marked *(verified)* was run on this machine, unelevated, on 2026-09-07.

## 1. What the report gets right, and what this repo already does

The report's most important architectural claim is correct, and is worth stating plainly because
it is the claim a naive implementation gets wrong: **do not reassemble a driver from
`System32\spool\drivers`.** Export the package Windows already staged, unmodified. Any edit to a
catalog-covered file invalidates the signature.

That claim is not new to this repo. `internal/install/package.go` already implements the report's
"supported reconstruction path" end to end — verify pinned SHA-256, verify the vendor Authenticode
signature, enumerate archive entries and reject unsafe paths, extract, verify the *catalog*
signature separately, `pnputil /add-driver` to stage, then `Add-PrinterDriver` to register, then
verify registration before any queue command runs. The report presents the staging/registration
split as a design insight; it is already the shipped code path.

Three more of the report's proposals are already implemented here:

- **Shared-resource refcounting on uninstall.** The report proposes checking whether a port or
  driver is still referenced before removing it. `uninstallCommands` in `reconcile.go` already
  does exactly this, and additionally refuses to remove a port it did not name (the
  `RAW9100-` prefix check).
- **Desired-state detection over a marker key.** The report correctly warns against
  `Test-Path HKLM:\Software\SpoolSmith\...`. `installCommands` already re-reads live Windows
  inventory and re-checks host address, port number, and protocol at mutation time.
- **Binding execution to a reviewed plan.** `InstallOptions.ExpectedPlan` already refuses to
  execute a plan that differs from the one previewed.

One report claim was confirmed by measurement rather than assumed. Exporting `oem15.inf` produced
**115 files against 116 in the FileRepository directory** *(verified)*. The one excluded file is
the machine-local precompiled `.PNF`. The export really is the package, not a directory copy —
which is the distinction the report is built on, and it holds.

## 2. Confirmed defects in the report

### 2.1 HIGH — the capture script correlates on a field that is empty in practice

The report's flagship capture snippet resolves the queue's driver through `Win32_PrinterDriver`,
and its artifact table claims that class "exposes `ConfigFile`, `DataFile`, `DependentFiles`,
`DriverPath`, `FilePath`, `InfName`, and `MonitorName`."

On this machine *(verified)*, for the real Brother driver:

```
Name           : Brother HL-L2315D series,3,Windows x64
InfName        :
MonitorName    :
FilePath       :
DriverPath     : C:\WINDOWS\system32\spool\DRIVERS\x64\3\BRPRM13A.DLL
```

`InfName` — the single field the report needs for package correlation — is **empty**, as are
`MonitorName` and `FilePath`. A capture built on this snippet resolves nothing, and would drop a
completely unambiguous driver into the report's own "Ambiguous / 50–79" bucket.

### 2.2 HIGH — the recommended native winspool work is unnecessary for the MVP

The report rates a native `GetPrinterDriver` level 6/8 wrapper as "preferable long-term" because
`DRIVER_INFO_8` carries the INF path, provider, manufacturer, hardware ID, print processor and
dependencies, and it treats the PrintManagement module as unable to supply them.

That is wrong on this platform. `Get-PrinterDriver` already returns all of it *(verified)*:

```
InfPath                : C:\WINDOWS\System32\DriverStore\FileRepository\brohl13a.inf_amd64_e477ef8d79b8572c\brohl13a.inf
Manufacturer           : Brother
provider               : Brother
HardwareID             : usbprint\brotherhl-l2315d_ser232a
PrintProcessor         : winprint
Monitor                :
CoreDriverDependencies :
IsPackageAware         : True
PrinterEnvironment     : Windows x64
```

The hardest correlation step in the entire report — queue → exact Driver Store INF — is one cmdlet
property. Budgeting a `winspool.drv` cgo wrapper against it is unjustified cost, and it would work
against the architecture rule that detection logic stays OS-independent and testable without
Windows.

### 2.3 MEDIUM — the report never names the correlation step that is actually missing

`InfPath` gives the FileRepository path. `pnputil /export-driver` takes the **published**
`oem#.inf` name. The report discusses `oem#.inf` at length as a portability hazard but never
specifies how to get from one to the other, which is the one genuinely fiddly step.

The working algorithm, confirmed on this machine *(verified)*: parse `pnputil /enum-drivers /class
Printer` and match `Original Name` against `basename(InfPath)`, then confirm with `Provider Name`
and `Driver Version`. Here that resolves cleanly:

```
Published Name:     oem15.inf
Original Name:      brohl13a.inf
Provider Name:      Brother
Driver Version:     10/18/2016 1.11.0.0
Attributes:         Legacy
```

The FileRepository directory hash (`..._amd64_e477ef8d79b8572c`) is available as a second
independent confirmation. Only two printer-class packages exist on this machine, so ambiguity is
rare in practice — but the match must still be proven rather than assumed.

### 2.4 MEDIUM — the recommended signature tool is not present where SpoolSmith runs

The report's security section makes `signtool verify /kp driver.cat` the verification control.
`signtool.exe` is **not installed** on this stock Windows 11 Pro machine *(verified — `Get-Command
signtool.exe` returns nothing)*. It ships with the Windows SDK, not with Windows.

This repo already made the right call: `package.go` verifies with `Get-AuthenticodeSignature`,
which is in-box. Run against the exported catalog it returns `Status : Valid`, `StatusMessage :
Signature verified.` *(verified)*. No change is needed here — but a spec written from the report
would introduce an SDK dependency on every endpoint for no gain.

### 2.5 MEDIUM — treating signature validity as a date check would fail-close on working drivers

The exported Brother catalog verifies as `Valid`, but its signer certificate reads:

```
Not Before : 10/12/2016 1:32:53 PM
Not After  : 1/5/2018 12:32:53 PM
```

*(verified)*. The certificate expired over eight years ago; the signature is still valid because of
countersigned timestamping. Any implementation that adds its own "certificate is not expired" check
on top of Windows' verdict will reject a legitimately signed, currently installed, physically
working driver — the exact driver the operator uses today. Defer to Windows' own chain policy
result and record it; do not re-derive it.

This is the kind of well-meant extra check a fail-closed project is most likely to add, which is
why it is called out rather than left implicit.

### 2.6 MEDIUM — `Get-PrinterProperty` yields nothing here

The report lists the printer property bag as a core capture surface with "Medium" portability, and
builds `EnumPrinterDataEx`/`GetPrinterDataEx` work on top of it. Against the live `Brother Home`
queue, `Get-PrinterProperty` returns **no rows at all** *(verified)*. It cannot be load-bearing in
a capture design, and native property-bag work should not be scheduled on the strength of this
report.

### 2.7 MEDIUM — the PrintTicket/DEVMODE plan is partly redundant

The report proposes native `PTConvertDevModeToPrintTicket` work to capture settings beyond
`Get-PrintConfiguration`. But `Get-PrintConfiguration` already returns `PrintCapabilitiesXML`
inline, including Brother's own private schema namespace
(`xmlns:brpsk="http://schemas.brother.info/mfc/printing/2006/11/printschemakeywords"`)
*(verified)*. Vendor capability data is already reachable with one cmdlet.

The genuine gap is the raw **DEVMODE** blob, which PowerShell does not expose. If native work is
ever justified, scope it to DEVMODE alone — and the report's own warning then applies: a private
DEVMODE is only meaningful against the same driver package, so it must be bound to the package
fingerprint or not captured at all.

### 2.8 LOW — `DriverVersion` is a packed integer the report's schema would serialize unusably

`Get-PrinterDriver` returns `DriverVersion : 281522221350912`. Decoded as four packed 16-bit fields
that is `1.11.0.0` *(verified)*, matching pnputil's `1.11.0.0`. The report's profile schema carries
`"version": "..."` with no note that decoding is required. Written naively, the profile gets an
opaque integer that cannot be compared against a target machine's package.

### 2.9 Positive finding — capture needs no elevation

Recorded because it decides the governance question in section 3.
`pnputil /export-driver oem15.inf <dir>` **succeeded with exit code 0 while unelevated**
*(verified — `IsInRole(Administrator)` returned `False` in the same session)*, exporting 115 files
and 25.9 MB.

Every read the capture design needs — `Get-Printer`, `Get-PrinterDriver`, `Get-PrinterPort`,
`Get-PrintConfiguration`, `pnputil /enum-drivers`, `pnputil /export-driver` — runs unprivileged and
mutates nothing. `PrintBrm.exe` is also present in-box at `System32\Spool\Tools` on this client SKU
*(verified)*, so the report's differential-oracle suggestion is usable if wanted.

## 3. Governance — the deployment half is not a diff

`CLAUDE.md` and D-0040 authorize local OS mutation narrowly. The report's Intune model breaks that
authorization in four distinct places, and one of them is marked in `CLAUDE.md` as not negotiable
by a future diff.

| Report proposes | Current authorization | Status |
| --- | --- | --- |
| Intune Win32 app installs the queue in System context, unattended, no human present | "no install ever skips the one required confirmation of a shown plan — this line survived D-0040 unchanged and is not up for negotiation by a future diff" | **Direct conflict.** There is no operator at an Intune endpoint to confirm anything. |
| Intune re-evaluates and reapplies on its own cycle | "never on a schedule or in response to anything but an explicit, one-time confirmed command" | **Direct conflict.** Reapplication is scheduled by definition. |
| SpoolSmith ships vendor driver bytes inside a distributable `.intunewin` | Trust model: the installer "must already be staged locally"; `profiles/` is gitignored and no payload is committed today | **New scope.** SpoolSmith becomes a payload distributor rather than a local installer. |
| Deploy the captured package to *other* machines | D-0040 authorizes local install on the machine the operator confirmed at | **New scope.** |

The report also flags the licensing question itself (its Epson example), and it is right to: this
repo distributes no vendor bytes today, and embedding them changes the project's distribution
posture, not just its code.

Against that, **capture is already inside authorization**: read-only, unelevated (2.9), mutates
nothing, and a natural extension of the `profile capture` command D-0041 already authorized. It
needs no new decision.

One further conflict is a design point rather than a scope point. The report proposes a 0–100
portability score with invented weights (40% package resolution, 20% integrity, 15% architecture,
15% dependency closure, 10% round-trip) that gates automatic deployment above 95. `CLAUDE.md` bans
exactly this shape: "No fuzzy match ever installs anything silently... Confidence and evidence are
always inspectable." A numeric score is neither falsifiable nor inspectable — it compresses the
evidence away, which is the specific failure the milestone-one definition of done was written to
prevent. Keep the report's *classification* (verified / needs review / blocked) and its *reasons*
list; drop the percentage.

## 4. Recommendation

Split the report and take half of it.

**Adopt now, no new decision required — capture only.** Extend `Profile` with a driver-package
identity block recording: published `oem#.inf`, original INF name, provider, manufacturer, decoded
version, driver date, environment, hardware ID, FileRepository directory, catalog file name,
Windows' own signature verdict, and a SHA-256 manifest of the exported files. Record identity and
hashes; do not put payload bytes in the repo or the profile. Resolution follows 2.2 and 2.3:
`Get-PrinterDriver.InfPath` for the package, `pnputil /enum-drivers` for the published name, both
required to agree.

**Adopt the dependency audit — it is the report's best idea and it is cheap.** Everything its
failure matrix needs is already in `Get-PrinterDriver` output: `Monitor`, `PrintProcessor`,
`CoreDriverDependencies`, `DependentFiles`, `PrinterEnvironment`. Classify each against the in-box
set — this machine has only `Appmon`, `Local Port`, `Standard TCP/IP Port`, `USB Monitor`,
`Virtual Port Monitor`, `WSD Port`, and `winprint` *(verified)* — and fail closed on anything
third-party that the exported package does not itself contain. No native code required.

**Do not build from this report:** `.intunewin` packaging, System-context unattended install,
payload embedding, the confidence score, the winspool cgo wrapper, `signtool`, and the
`EnumPrinterDataEx` property-bag work.

**Take the timeline seriously, but separately.** The report's read on Microsoft's servicing
direction is the strategically important part, and it is independent of everything above. Note that
this machine's Brother package already reports `Attributes: Legacy` *(verified)*, and that
Protected Print Mode is not enabled here — no WPP policy key is present *(verified)*. Whether
SpoolSmith should gain an IPP/Windows Ready Print profile class is a product question for the
operator, not a finding against the code.

If the operator wants the Intune direction, it needs its own decision record answering one question
first: **what replaces the confirmation gate when no human is present at the endpoint?** Until that
is answered, there is nothing to implement.
