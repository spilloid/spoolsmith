# SpoolSmith MSI

`spoolsmith.wxs` defines a small, machine-wide Windows Installer package:

- installs `spoolsmith.exe`, `spoolsmith-gui.exe`, `README.md` and `LICENSE`
  to `C:\Program Files\SpoolSmith\` (per-machine, x64, administrator install);
- adds one Start Menu shortcut, **SpoolSmith**, to `spoolsmith-gui.exe`;
- registers in Apps & Features as **SpoolSmith**, publisher **Joseph Spillers**
  (the same name the executables are signed with);
- nothing else: no services, scheduled tasks, updater, PATH change, shell
  extension, registry values or file associations.

The `.ssb` association stays where v1.3.0 put it: the app offers it per user,
under `HKCU\Software\Classes`, and the MSI never registers it machine-wide.
Uninstalling the MSI does not remove a user's association (Windows Installer
cannot reach every user's profile); it then points at a missing program and
Windows asks which app to use. Turn it off first under **More → Open .ssb files
with SpoolSmith** if that matters.

The MSI is packaging only. Printer Intune packages made by `intune build` /
the wizard carry their own pinned SpoolSmith CLI and never depend on this MSI.

## Identity: read before changing anything here

| Field | Value | Rule |
|---|---|---|
| UpgradeCode | `{62631A50-5FE9-4696-B410-92BEAAE13C52}` | **Never changes.** It is how every SpoolSmith MSI finds the one before it. A new UpgradeCode would install side by side with older versions. |
| ProductCode | `*` (new for every build) | Every build is a major upgrade: it removes the installed SpoolSmith and installs itself. |
| ProductVersion | `X.Y.Z` from tag `vX.Y.Z` (`msi-version.sh`) | Windows Installer compares the first three fields only, within 255.255.65535; tags outside that are refused. |
| Upgrade rule | `MajorUpgrade AllowSameVersionUpgrades="yes"` | Newer replaces older. Older refuses to install over newer (exit 1603, "A newer version of SpoolSmith is already installed"). A rebuild of the same version replaces the first build instead of installing beside it. |
| Component GUIDs | fixed, one per file | Each names the same file at the same path forever. Adding a file means adding a component with a **new** GUID; never reuse or change an existing one. |
| Install directory | `ProgramFiles64Folder\SpoolSmith` | Changing it breaks upgrades that expect files in place; don't. |

`check-msi.sh` asserts all of this on every build.

## Tooling and license

The MSI is built with **wixl** from GNOME **msitools** (LGPL-2.1-or-later), from
the Ubuntu package, not with the WiX Toolset: current WiX binary releases carry
an Open Source Maintenance Fee for commercial use, which this project avoids.
wixl reads the WiX v3 schema subset this installer uses. It was evaluated
against every requirement and covers them: per-machine x64 install, a
non-advertised shortcut with an explicit target, `MajorUpgrade` with downgrade
refusal and same-version upgrades, and compressed embedded cabinets. The CI
install, upgrade, downgrade and uninstall tests are the evidence. One quirk:
a `Shortcut` nested inside a `File` came out advertised despite
`Advertise="no"`, so the shortcut sits in the component with
`Target="[#SpoolSmithGuiExe]"`.

## Building

On Ubuntu or Debian:

```sh
sudo apt-get install wixl msitools
installer/build-msi.sh v1.3.0 <payload-dir> <out-dir>
```

`<payload-dir>` holds exactly the four release files. Releases never build
application binaries for the MSI: `.github/workflows/msi.yml` unpacks the
signed release ZIP (after checking it against its published SHA-256 and
verifying the EXE signatures), builds the MSI from those bytes, signs the MSI
with the same Azure Artifact Signing profile, and proves the result: valid
timestamped signature, signed EXEs inside identical to the ZIP's, and a real
quiet install, run and uninstall on a throwaway runner.

## Checking an upgrade by hand

CI installs v0.0.1 then v0.0.2 built from the same payload, checks that one
SpoolSmith remains at 0.0.2, that v0.0.1 then refuses to install, and that a
second build of v0.0.2 replaces the first. To repeat that with real releases on
a test machine:

```powershell
msiexec /i SpoolSmith-v1.3.0-x64.msi /qn /norestart /l*v install-old.log
msiexec /i SpoolSmith-v1.4.0-x64.msi /qn /norestart /l*v upgrade.log
Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*' |
  Where-Object DisplayName -eq SpoolSmith | Select-Object DisplayName, DisplayVersion, PSChildName
# expect exactly one row, DisplayVersion 1.4.0
msiexec /i SpoolSmith-v1.3.0-x64.msi /qn /norestart   # expect exit code 1603
msiexec /x SpoolSmith-v1.4.0-x64.msi /qn /norestart
Test-Path 'C:\Program Files\SpoolSmith'               # expect False
```
