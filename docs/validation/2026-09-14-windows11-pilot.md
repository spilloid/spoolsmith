# Windows 11 VM pilot: offline provisioning and Intune endpoint lifecycle

Status: **executed 2026-09-15 on a standalone VM; tenant cases still outstanding**.
Results are recorded in [2026-09-15-windows11-results.md](2026-09-15-windows11-results.md).
This is the execution runbook for issues #5 and #6, paired with the
[deployment tutorial](../intune-deployment.md),
[implementation reflection](../offline-intune-reflection.md), and
[result template](windows11-results-template.md).

## Build and test boundaries

Test implementation commit `20ab179` on `codex/offline-intune` first. The original
main checkout has separate unfinished clone/bundle work; it is not this pilot build.
The pilot ZIP is under `dist/spoolsmith-offline-intune-pilot.zip` in this worktree.

ZIP SHA-256:
`45cb2c69070b83766414e202c6e9581c2654a5c97a6cd8402fb7f88542a616f7`

The archive contains both Windows executables, the GUI manifest, binary checksums,
a deployment tutorial, and an illustrative generated app. That example's driver
and identity are invented. Do not count installing a fixture queue as a real-device
compatibility or printing result.

A standalone VM can validate the native CLI, PowerShell 5.1, ACLs, SYSTEM execution,
local detection, recovery and GUI behavior. Intune enrollment and tenant assignments
are additional prerequisites for Required/Available/Company Portal results. Physical
printing additionally requires a real supported driver and a route to the printer.
Record each of those separately.

## 1. Attach and establish the baseline

Record the VM's SSH host/port, login identity, authentication method and host-key
fingerprint in the private session notes. Keep credentials out of repository files.
Use the operator-enabled PowerShell SSH shell. If its behavior differs, inspect the
configured shell using Microsoft's [Windows OpenSSH reference](https://learn.microsoft.com/en-us/windows-server/administration/openssh/openssh-server-configuration);
do not change SSH configuration merely to make a test pass.

Run these read-only checks in the actual SSH session:

```powershell
$PSVersionTable
[Environment]::Is64BitProcess
$env:PROCESSOR_ARCHITECTURE
whoami /user
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
$principal.IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
Get-CimInstance Win32_OperatingSystem | Select-Object Caption, Version, BuildNumber, OSArchitecture
Get-Service Spooler
Get-Command Get-Printer, Get-PrinterPort, Get-PrinterDriver
Get-Printer | Select-Object Name, DriverName, PortName
Get-PrinterPort | Select-Object Name, PrinterHostAddress, Protocol, PortNumber
Get-PrinterDriver | Select-Object Name, PrinterEnvironment, InfPath
```

An SSH login's actual token and process architecture are evidence; the fact that
login succeeded is not enough. Run the endpoint scripts with inbox 64-bit
`powershell.exe` (Windows PowerShell 5.1), even if another PowerShell is installed.
Use an interactive RDP desktop for GUI observations; a successful SSH process
launch does not establish that the window is usable in the desktop session.

Create a VM snapshot before driver/queue mutations. Use a dedicated test queue
name and deployment ID, such as `SpoolSmith VM Accounting` and `vm-accounting`.
Do not reuse a queue that was present in the baseline.

## 2. Stage the exact pilot and capture evidence

Use a fresh administrator-owned lab directory such as `C:\SpoolSmithLab`.
Before registering a SYSTEM task, restrict the lab scripts and inputs to SYSTEM
and Administrators; do not execute privileged tests from a standard-user-writable
shared folder. Preserve the original ZIP separately from the extracted source.
Verify its SHA-256 against the value above before using `Expand-Archive`.

Suggested layout:

```text
C:\SpoolSmithLab\
  incoming\spoolsmith-offline-intune-pilot.zip
  spoolsmith\                      extracted pilot
  profiles\                        actual validated profiles
  apps\                            generated test app sources
  evidence\                        local inventory, outputs, screenshots, results
```

From the extracted `spoolsmith` directory:

```powershell
Get-FileHash .\spoolsmith.exe, .\spoolsmith-gui.exe -Algorithm SHA256
Get-Content .\SHA256SUMS.txt
.\spoolsmith.exe capabilities
```

Check the hashes individually against `SHA256SUMS.txt`. Keep command stdout,
stderr and exit codes in separate files for each case, and save before/after
queue/port/driver inventories. PowerShell's native command exit code must be read
immediately after that command, before running another native program.

## 3. Validate offline CLI behavior with a real driver

Choose the driver from the VM's actual registered driver inventory. Prefer the
reviewed Brother archive path when the approved local payload is available; record
its hash and signature result. A generic fixture driver only establishes queue
mechanics, not printer compatibility. Do not remove drivers already used by baseline
queues to create a missing-driver test; use a fresh snapshot instead.

Capture and review a profile while the printer is reachable, using the exact
registered driver name, then confirm the normal strict path. Save the profile under
`profiles\accounting.json`. The capture command is:

```powershell
.\spoolsmith.exe profile capture <printer-ip> C:\SpoolSmithLab\profiles\accounting.json --name 'SpoolSmith VM Accounting' --driver '<exact registered driver name>'
```

If the printer is not available, label a separately constructed profile as a
**fixture used for local Windows mechanics** in the result record. Do not present
it as a real capture or mark physical-print acceptance complete.

Make only the printer target unreachable for the offline test; preserve the SSH
and RDP management path. Record how reachability was removed. Never disable the
VM's only management adapter from the remote test shell.

```powershell
$cli = 'C:\SpoolSmithLab\spoolsmith\spoolsmith.exe'
$profile = 'C:\SpoolSmithLab\profiles\accounting.json'
& $cli add --profile $profile --offline --dry-run --json
& $cli add --profile $profile --offline --yes --json
& $cli status --profile $profile --json
& $cli add --profile $profile --offline --yes --json
& $cli configure --profile $profile --offline --yes --json
& $cli add --profile $profile --yes --json
```

Run each line as its own recorded case, not as an unattended batch. Expectations:
dry-run changes no inventory; offline add/configure succeed and report local
verification; repeated application produces no duplicate queue or port; status
passes while unreachable; the final strict command fails without falling back.
Also verify missing profile, positional target, forced-family and missing-driver
rejections. Run without `--yes` in a noninteractive session and verify exit 5 with
no mutation or prompt. Restore reachability before a separately recorded print test.

## 4. Run the generated app under Administrator, then SYSTEM

Generate a fresh app from the tested profile and pinned CLI using the wizard or
`intune build`. Use `vm-accounting`, revision 1, and offline mode. Start from the
clean baseline snapshot so first-install ownership is unambiguous. Explicit
adoption is its own later case.

First invoke the exported `install.ps1` in elevated PowerShell 5.1. Inspect the
actual ACLs and ownership under `%ProgramData%\SpoolSmith`, plus `current.json`,
revision directories and logs. Test that a separate standard-user session cannot
modify the retained executable, profile, runtime or metadata. Do not infer access
control correctness from the presence of an ACL-setting command in a script.

For a SYSTEM test, use a one-shot task in this disposable VM, with scripts located
in the protected lab directory. A task with no schedule trigger can be started
explicitly. This uses the service-account principal described in Microsoft's
[ScheduledTasks documentation](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtaskprincipal?view=windowsserver2025-ps).
Example registration for an already reviewed test app:

```powershell
$taskName = 'SpoolSmith-VM-Pilot-Install'
if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
    throw 'Pilot task name already exists; inspect it before proceeding'
}
$action = New-ScheduledTaskAction `
    -Execute 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' `
    -Argument '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "C:\SpoolSmithLab\apps\accounting-r1\install.ps1"' `
    -WorkingDirectory 'C:\SpoolSmithLab\apps\accounting-r1'
$taskPrincipal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 15)
Register-ScheduledTask -TaskName $taskName -Action $action -Principal $taskPrincipal -Settings $settings
Start-ScheduledTask -TaskName $taskName
```

Poll `Get-ScheduledTask` and `Get-ScheduledTaskInfo` until a recorded run completes;
a successful `Start-ScheduledTask` only means the start request succeeded. Save
`LastRunTime` and `LastTaskResult`, CLI logs, and independently queried inventory.
Capture the task principal and a SYSTEM identity check as part of the evidence.
After completion and evidence collection, unregister only this test task:

```powershell
Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
```

Run `detect.ps1` in a separate 64-bit SYSTEM test task with stdout/stderr redirected
to separate evidence files by a reviewed wrapper. Verify nonempty stdout plus exit
0 only for a matching revision and local queue. Check absent queue, absent driver,
wrong target/protocol/port, pending state, and failed inventory separately. A
scheduled task validates the endpoint context; it does not validate Intune delivery.

## 5. Prioritized lifecycle and failure cases

Use snapshot restores between destructive fault cases. Record the exact changed
resource and recover it before advancing to the next case.

| Case | Expected result / evidence |
| --- | --- |
| Repeat same revision | One queue/port, compliant detection, no prompt |
| SYSTEM first creates state, Admin repeats; then reverse | Both legitimate contexts can use protected state |
| Existing unmanaged queue, adoption disabled/enabled | Reject by default; adopt only exact match when explicitly enabled |
| Same queue claimed by another deployment ID | Reject even with adoption enabled |
| Revision 2 changes target or driver | Reviewed update; verify actual new configuration and retain shared resources |
| Same-revision change, downgrade, queue rename | Reject without changing the queue |
| Interruption after mutation, before state commit | Pending state blocks detection; retry/removal remains possible |
| Failure after port creation, before queue creation | Record the known orphan-port cleanup gap; inspect removal behavior |
| Wrong archive/binary hash before root initialization | Reject; preserve stderr externally because deployment log may not exist |
| Spooler unavailable or missing registered driver | Clear failure; never compliant detection |
| Lock contention | Exit 1618 without printer mutation |
| Timeout with a spawned child | Record elapsed time, exit, descendant termination and recoverability |
| Existing path with untrusted ownership or reparse point | Refuse; do not weaken ACL checks to proceed |
| Non-ASCII queue/driver names | Correct PowerShell 5.1 JSON and exact local comparison |
| 32-bit wrapper entry | Relaunch as x64; preserve actual exit status |
| Source cache removed after installation | Persisted uninstall works, including a second removal |
| Second queue shares the driver and/or managed port | Other queue, shared port and driver survive removal |

For cache-independent removal, retain the original ZIP in `incoming`, remove only
the disposable generated app source after successful installation, and invoke the
exact persisted uninstall command printed in the app's manifest. Do not remove
ProgramData revision files before uninstall. Snapshot recovery is available if a
case leaves the test queue in a state the conservative removal path refuses.

## 6. Desktop and tenant cases

Through RDP, exercise all three wizard steps, profile/binary selection, Unicode
names and paths, incorrect hash/driver selection, changing inputs after preview,
cancel, export, and refusal to overwrite an existing output directory. Check
minimum window size and the VM's display scaling. Save screenshots of meaningful
states, after removing incidental personal or tenant information from the frame.
Compare the exported manifest to the choices actually shown.

If this VM is Intune-enrolled, continue with the tutorial's Required deployment,
Available standard-user install, Company Portal uninstall/dependency behavior,
and explicit Uninstall assignment cases. Otherwise mark those **blocked: tenant
pilot unavailable**. Do not substitute a scheduled task for these results.

Finish by restoring printer connectivity, printing to the real device and recording
the operator's physical observation. Capture residual test resources and retained
logs before cleanup. Fill the result template with pass/fail/blocked/not-run,
commit/hash and evidence paths; do not close either issue from a bare success code.
