# Intune packaging

`spoolsmith intune wizard`/`intune build` are supported: they export a reviewable
Win32 app package — install/uninstall/detect scripts, a protected local deployment
record, and the exact commands and detection rule to paste into Intune. This is,
deliberately, as far as SpoolSmith goes: it never authenticates to a tenant,
uploads an app, creates or assigns a group, or downloads a driver. Creating the
Win32 app in Intune, uploading the package, and assigning it stay manual steps in
the Intune admin center — see "Prepare and upload" and "Required and Company
Portal" below. Tenant sign-in and automatic upload/assignment are a distinct,
broader feature that isn't planned; see the [roadmap](roadmap.md). The desktop
GUI offers the same wizard (Tools tab → "Build an Intune printer app..."), for
anyone who'd rather not use the CLI — validated on real Windows hardware in
[the UX-simplification record](validation/2026-09-18-gui-intune-ux-simplification.md)
(building on [the original dialog's validation](validation/2026-09-17-gui-intune-wizard.md)).

[Windows/SYSTEM validation on September 15](validation/2026-09-15-windows11-results.md)
covered installation, local detection, protected state, standard-user denials,
revision updates and cache-independent removal — everything SpoolSmith itself
does. Several lifecycle cases remain unrun. **Real Intune tenant delivery and
Company Portal validation** (the manual steps a human performs afterward) **are
still pending** and not required to ship the packaging step above.
Automated script tests are supplementary and are not tenant evidence.

Use the [Windows pilot runbook](validation/2026-09-14-windows11-pilot.md) and actual
[results](validation/2026-09-15-windows11-results.md) to identify the remaining cases.
The [pre-pilot reflection](offline-intune-reflection.md) records earlier concerns;
use the dated results for their current validation status.

## Prepare a validated profile and binary

Use a currently supported Windows x64 endpoint with the Print Spooler and PrintManagement
cmdlets available. Prepare an enrolled Intune pilot device, appropriate Intune
licensing, and an administrator permitted to create and assign Win32 apps.
See Microsoft's [Win32 prerequisites and setup](https://learn.microsoft.com/en-us/intune/app-management/deployment/add-win32).

1. On the printer's network, capture a profile and validate the selected driver's
   compatibility by installing and printing. Review the exact queue name, literal
   IP address, and registered driver name. Captured identity is descriptive evidence,
   not device authentication.
2. Supply either a profile referencing the supported pinned Brother local archive
   recipe or explicitly accept a separately managed registered-driver prerequisite.
   Arbitrary OEM installers, downloaded payloads, and clone/export bundles are not
   supported by this packaging path. Both the archive hash and Windows signature
   checks remain mandatory when staging the supported archive. Keeping a driver
   registered does not prove it is compatible with the printer; validate that first.
3. Build the CLI. `spoolsmith capabilities` must report `intune-endpoint-v1`; an
   older release binary or the GUI executable is refused by `intune build`/`wizard`.

```powershell
go build -o spoolsmith.exe ./cmd/spoolsmith
.\spoolsmith.exe capabilities
```

SpoolSmith calculates the selected binary’s SHA-256 automatically and shows it
in the review manifest. You can supply an approved hash with `--binary-sha256`
or in advanced settings; a mismatch is refused. Pinning prevents accidental
payload changes between preview and export; it is not a publisher signature.
Use only binaries and driver payloads approved by your organization. The Windows
x64 PE, Go CLI build identity and endpoint capability checks still run, and the
CLI and any driver archive are rechecked against their pins during export.

## Run the wizard

The interactive CLI and desktop GUI use the same defaults:

1. Choose the validated profile and approved CLI executable. The app name comes
   from the profile’s queue name; a description includes its name and address.
   Revision starts at 1. A stable deployment ID is suggested from the queue name,
   and the binary hash is calculated automatically. If the profile has no local
   archive, explicitly accept that the driver will be registered separately.
2. Review the destination and manifest, including the ID, commands, hashes,
   filenames, target, driver and policy. Type `export` in the CLI or click
   **Export reviewed package** in the GUI to create the local package.

```powershell
.\spoolsmith.exe intune wizard
```

The desktop GUI’s Tools tab → **Build an Intune printer app...** now has two
pages: **Package settings** and **Review and export**. The common path needs only
file selection, the driver prerequisite choice if applicable, and review/export.
App name and destination are editable on the first page. **Advanced settings**
contains ID, revision, optional location, description, an approved hash override,
offline provisioning and adoption. Returning to settings or editing any package
input invalidates the previous preview; validate again before exporting.
The CLI wizard offers the same advanced settings when requested.

Live identity validation and no adoption remain the defaults. Offline provisioning
and adoption of an exactly matching unmanaged queue require explicit choices.
Offline skips live identity checks at installation; printing still needs network
connectivity. A failed strict probe never falls back to offline mode.

The suggested ID combines a readable queue-name slug with a short hash of the
exact queue name, preserving distinctions between punctuation, Unicode and long
names. It does not depend on profile filename, address, driver or app display
name. **For an existing deployment, reuse its ID (including any previously chosen
custom ID) and increase the revision for configuration changes.** The tool does
not look up deployed revisions. Location is left blank because profiles do not
contain that information.

The suggested destination is an unused `<id>-r<revision>` directory beside the
profile. If occupied, `-2`, `-3`, etc. are appended. These suffixes distinguish
export folders; they do not increment deployment revisions. Suggestions create
nothing. Export creates exactly the reviewed directory and refuses it if another
process creates it first. An explicit output path must also be new, with an
existing parent directory.

For scripted packaging, `intune build` keeps its explicit flag overrides. Only
`--profile` and `--binary` are required for a profile with a supported archive;
this example accepts the separately managed driver prerequisite. Start with
`--dry-run` to review the manifest and suggested destination without creating
files or running Microsoft’s tool:

```powershell
.\spoolsmith.exe intune build `
  --profile .\profiles\accounting.json `
  --binary .\spoolsmith.exe --driver-prerequisite --dry-run
```

After review, repeat without `--dry-run` to export. For automation that separates
preview and export into different invocations, pass the reviewed
`--binary-sha256` and `--output` to keep the same binary pin and destination.
Use the interactive wizard to retain the prepared package and pins in memory
between review and confirmation. `build` retains its noninteractive export
behavior; it does not prompt.

Override defaults when needed, especially when updating an existing deployment:

```powershell
.\spoolsmith.exe intune build `
  --profile .\profiles\accounting.json `
  --binary .\spoolsmith.exe --binary-sha256 '<approved SHA-256>' `
  --id indy-accounting --revision 2 --name 'Accounting Copier' `
  --location Indianapolis --description 'Accounting department copier' `
  --driver-prerequisite --offline --output .\accounting-r2 --dry-run
```

Omit `--driver-prerequisite` when the profile includes an approved local archive.
Explicit metadata overrides are preserved; `--description=` leaves the
optional description empty.

The export contains `profile.json`, `deployment.json`, `spoolsmith.exe`,
`install.ps1`, `uninstall.ps1`, `runtime.ps1`, `detect.ps1`, and `README.txt`,
plus `driver.exe` when using the supported archive. Existing output directories
are refused. The sample [profile](../examples/intune/accounting.json) is illustrative
and must be replaced with an actual captured, validated profile before deployment.

## Prepare and upload

Download Microsoft's current [Win32 Content Prep Tool](https://github.com/microsoft/Microsoft-Win32-Content-Prep-Tool)
and keep it outside the source bundle directory. Run:

```powershell
.\IntuneWinAppUtil.exe -c "<exported-folder>" -s install.ps1 -o .\intunewin -q
```

Alternatively pass `--content-prep-tool <path>` and
`--content-prep-output <separate-directory>` to `intune build`. Without those
options SpoolSmith exports the source and explicitly reports the preparation
prerequisite; it does not claim to have created an `.intunewin` file.

Create a Windows app (Win32) in Intune and upload the resulting package.
Copy the exact commands from the exported `README.txt` or `deployment.json`.
Use **System** install behavior, x64 requirements, and a 15-minute installation
limit. The install wrapper relaunches in 64-bit PowerShell where necessary. Each
CLI invocation is bounded to ten minutes; a timeout terminates its process tree.

The uninstall command resolves CommonApplicationData inside PowerShell and calls
the retained uninstaller, so it does not depend on environment expansion in
Intune's uninstall field or the original cache directory.

Upload `detect.ps1` as a custom detection script and choose **No** for running
as a 32-bit process on 64-bit clients. Detection succeeds only when the intended
revision and actual queue, registered driver, target, RAW protocol, and port 9100
match. It checks no reachability and makes no printer-network request. On mismatch,
missing state, pending installation, or inventory failure it exits nonzero with no
stdout or stderr. Successful detection emits stdout and exits zero. This follows
Microsoft's [custom detection contract](https://learn.microsoft.com/en-us/intune/app-management/deployment/add-win32#step-4-detection-rules).

Generated scripts are UTF-8 with BOM and unsigned. Apply your organization's script
signing policy; enabling signature enforcement requires signing the exported
scripts. Intune should not request interaction from the endpoint user.

Configure these return codes (remove unrelated default success/reboot mappings):

| Code | Intune mapping | Meaning and action |
| --- | --- | --- |
| 0 | Success | Locally configured and verified, or explicitly removed/already absent |
| 1 | Failed | Inventory, conflict, package verification, execution or wrapper failure; inspect logs |
| 2 | Failed | Invalid profile, deployment inputs or arguments |
| 3 | Failed | Strict identity unresolved/mismatched; `status` uses this for local mismatch |
| 4 | Failed | Elevation/driver prerequisite failure; repair prerequisites |
| 5 | Failed | Confirmation contract failed; generated installers always supply approval flags |
| 1618 | Retry | Another SpoolSmith deployment holds the machine lock |
| 1460 | Retry | CLI execution timed out; inspect possible partial state before retry |

No reboot success codes are generated. A generic failed installation is not
silently classified as transient; signature and configuration errors require review.

## Required and Company Portal

Use Required assignments for automatic provisioning. Use Available assignments
for optional Company Portal installation; pilot with a standard user. Choose user
targeting for a person's optional printer choices and device targeting for shared
machines whose location determines the queue. Confirm Company Portal visibility
for the intended enrolled/primary user and shared-device arrangement. Microsoft's
[current assignment documentation](https://learn.microsoft.com/en-us/intune/app-management/deployment/assign-groups)
permits Available Win32 assignments to user or device groups.

SYSTEM-created queues are machine-wide. A group assignment controls app delivery,
not permission to print: apply printer/network authorization separately where needed.

For supported optional removal, enable **Allow available uninstall**. Microsoft
currently hides Company Portal uninstall when an app has dependencies or is itself
a dependent app, even with that setting enabled. A separately managed driver can
therefore affect this experience if modeled as an app dependency. Verify in the
pilot using the tenant's actual dependency layout. See
[Microsoft's program settings](https://learn.microsoft.com/en-us/intune/app-management/deployment/add-win32#step-2-program).

When deploying off-site, select offline mode and provide all prerequisites locally.
The queue should appear before a route to the printer is available. Test printing
later, when connectivity returns. Installation success is not a print-test result.

## Ownership, changes and retirement

The stable deployment ID owns one queue name. A first install refuses an existing
queue unless adoption was explicitly enabled and local configuration matches exactly.
A different deployment cannot adopt a claimed queue. A machine-wide file lock
serializes SpoolSmith deployment operations.

Keep the ID and queue name stable and increase the revision for a changed target,
driver, binary, or validation mode. Same-revision changes and downgrades are refused.
Updates validate the old or newly requested local configuration before reconciling
and verify the result afterwards. Arbitrary queue drift stops the update.
Existing conflicting ports are never overwritten. Previous ports remain in place
after address changes; an administrator can review unused ports for later cleanup.
Shared drivers are retained, and removal retains ports still used by another queue.

For a renamed queue, replacement deployment or department move needing a new name,
explicitly uninstall the old deployment and deploy a new ID/profile. Avoid assigning
both deployments concurrently. If the name stays the same, a printer replacement
can use a higher revision with an updated target and validated driver/profile.

Retirement requires an explicit Uninstall assignment or supported user uninstall.
**Removing a Required assignment does not remove the queue.** Driver and binary
revisions likewise need a newly exported package and updated detection script.

Protected state lives under
`%ProgramData%\SpoolSmith\Deployments\<id>`. SYSTEM and Administrators have access;
unprivileged writers and reparse-point paths are rejected. Each revision retains
its executable, profile, supported driver archive and removal helpers. Logs include
CLI stdout/stderr and a lifecycle log. A pending manifest is saved before printer
mutation and blocks detection until local verification and state commit succeed.

After an interrupted attempt, retry the same revision or explicitly remove it before
trying a different revision. Removal checks the pending/current candidate against
local configuration and refuses an unrelated changed queue. It works after the
original installer working directory is gone. It retains old revisions, logs and
helpers; administrator-reviewed disk cleanup is separate from printer removal.
Do not delete retained removal inputs while the deployment is active.

## Troubleshooting and pilot checklist

Start with `logs\lifecycle.log`, then the invocation's `*.stdout.log` and
`*.stderr.log`. `spoolsmith status --profile <profile>` reports local mismatches
without contacting the printer. Missing drivers require staging the approved archive
or registering the separately managed exact driver. Hash/signature failures require
an approved replacement payload, not a bypass. Spooler failures require restoring
Windows printing services. Reachability problems affect strict validation and
printing, and do not establish whether the local queue is configured correctly.

Record OS/build, architecture, CLI SHA-256, recipe/hash, tenant app ID, assignment
and observed results for each pilot case. No cases below have been certified by
this Linux development session:

- [ ] Elevated offline add with printer unreachable; queue visible locally.
- [ ] Reapply and configure without duplicate queues/ports; default mode still probes.
- [ ] Return to the printer's network and print successfully.
- [ ] SYSTEM Required deployment on an enrolled x64 Windows pilot.
- [ ] Standard-user Available installation through Company Portal, including off-site.
- [ ] Detection while unreachable; reject each wrong/missing queue, driver and endpoint field.
- [ ] Supported archive hash/signature rejection and absent registered driver.
- [ ] NTFS protection, reparse-path rejection, concurrent deployment lock and timeout logs.
- [ ] Higher-revision address/driver change; same-revision change, downgrade and rename refusal.
- [ ] Interrupted install/update, same-revision retry, and explicit failed-attempt removal.
- [ ] Removal after deleting the original cache directory; repeated removal.
- [ ] Another queue sharing the driver/port remains intact after removal.
- [ ] Existing-queue adoption, other-deployment conflict, and manual configuration drift.
- [ ] Company Portal uninstall with and without dependency restrictions.
- [ ] Explicit Uninstall assignment and retirement; removal of Required alone leaves the queue.
