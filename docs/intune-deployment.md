# Deploy one printer with Intune

SpoolSmith can export a local Win32 printer app using the desktop wizard
(**Tools → Build an Intune printer app**) or `spoolsmith intune wizard`.
The exported scripts run silently as SYSTEM and keep their removal inputs in
protected machine storage. Packaging does not authenticate to a tenant, upload
an app, assign groups, download a driver, or modify the administrator's printers.

This feature is implemented in source. **Windows/SYSTEM and Intune pilot results
are still pending.** The automated PowerShell tests exercise generated scripts
with substituted Windows inventory and privilege boundaries; they do not validate
NTFS ACL behavior, an OEM driver, Company Portal, or a tenant deployment.

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
3. Build the CLI and optional desktop app from this source. v0.4.0 lacks the new
   endpoint commands. The packager rejects an older or incompatible executable.

```powershell
go build -o spoolsmith.exe ./cmd/spoolsmith
go build -ldflags '-H windowsgui' -o spoolsmith-gui.exe ./cmd/spoolsmith-gui
.\spoolsmith.exe capabilities
(Get-FileHash .\spoolsmith.exe -Algorithm SHA256).Hash
```

Record the binary hash as the reviewed build pin. Pinning prevents accidental
payload changes between preview and export; it is not a publisher signature.
Use only binaries and driver payloads approved by your organization.

## Run the wizard

The desktop wizard has three steps:

1. Select the profile and CLI; enter or calculate the binary hash and review it.
   Select the separately managed driver prerequisite only when the profile has
   no bundled archive.
2. Set a stable ID such as `indy-accounting`, a positive revision, display name,
   location, description, and a new output directory. Choose strict live validation
   or explicitly choose offline provisioning. Allow adoption only if you intend
   to manage an existing queue whose full configuration matches.
3. Review commands, hashes, filenames, target, driver and policy. Export the package.

The terminal wizard asks for the same information:

```powershell
.\spoolsmith.exe intune wizard
```

For repeatable packaging, use explicit flags. `--dry-run` validates inputs and
prints the manifest without exporting files or running Microsoft's tool:

```powershell
.\spoolsmith.exe intune build `
  --profile .\profiles\accounting.json `
  --binary .\spoolsmith.exe --binary-sha256 '<reviewed SHA-256>' `
  --id indy-accounting --revision 1 `
  --name 'Indianapolis — Accounting Copier' --location Indianapolis `
  --description 'Accounting department copier' `
  --driver-prerequisite --offline --output .\accounting-r1 --dry-run
```

Remove `--dry-run` to export. Omit `--driver-prerequisite` when the profile includes
an approved local archive. Omit `--offline` to require live identity checks.
A failed strict probe never falls back to offline mode.

The export contains `profile.json`, `deployment.json`, `spoolsmith.exe`,
`install.ps1`, `uninstall.ps1`, `runtime.ps1`, `detect.ps1`, and `README.txt`,
plus `driver.exe` when using the supported archive. Existing output directories
are refused. The sample [profile](../examples/intune/accounting.json) is illustrative
and must be replaced with an actual captured, validated profile before deployment.

## Prepare and upload

Download Microsoft's current [Win32 Content Prep Tool](https://github.com/microsoft/Microsoft-Win32-Content-Prep-Tool)
and keep it outside the source bundle directory. Run:

```powershell
.\IntuneWinAppUtil.exe -c .\accounting-r1 -s install.ps1 -o .\intunewin -q
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
