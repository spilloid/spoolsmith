SpoolSmith Intune deployment: {{.DisplayName}}
ID: {{.ID}} / Revision: {{.Revision}} / Windows x64
Location: {{.Location}}
{{.Description}}

Queue: {{.Profile.PrinterName}}
Target: {{.Profile.Target}} (RAW TCP 9100)
Driver: {{.Profile.DriverName}}
Offline provisioning: {{.Offline}}
Separately managed registered driver required: {{.DriverPrerequisite}}
Adopt an existing exactly matching queue: {{.Adopt}}

Review deployment.json, profile.json and all PowerShell scripts before delivery.
Pin this locally built CLI by SHA-256: {{.BinarySHA256}}
It must include the offline and status commands (v0.4.0 does not).

1. Download Microsoft's current Win32 Content Prep Tool from its official source.
   Keep it outside this folder. Run from the parent folder:
   IntuneWinAppUtil.exe -c "<this folder>" -s install.ps1 -o "<separate output folder>" -q
2. Add the resulting .intunewin as a Windows app (Win32) in Intune.
3. Install behavior: SYSTEM. Requirement: Windows x64, supported Windows version.
   Installation timeout: 15 minutes (individual CLI calls time out at 10 minutes).
   Install command:
   {{.InstallCommand}}
   Uninstall command (works after the original cache is removed):
   {{.UninstallCommand}}
4. Detection: upload detect.ps1. Run as 32-bit on 64-bit clients: No.
   The generated scripts are unsigned; use your organization's signing policy.
   Success requires both exit 0 and stdout. Failures produce no stdout/stderr.
5. Return codes: 0 Success; 1,2,3,4,5 Failed; 1618 Retry; 1460 Retry.
   No reboot codes are generated. Remove unrelated default success mappings.
6. Pilot Required and Available assignments before broad rollout.

Lifecycle: keep the ID and queue name stable. Increase revision for configuration,
mode or binary changes. A lower revision or changed configuration at the same
revision is refused. An existing unmanaged queue requires explicit adoption and
an exact local match. Another deployment's queue cannot be adopted. Updates can
change address/driver but preserve old ports for safety. Rename: explicitly remove
the old deployment, then create a new ID/profile. Do not assign both concurrently.
Removing a Required assignment does not perform an uninstall; assign Uninstall.

ProgramData\SpoolSmith\Deployments\{{.ID}} retains protected profiles, binaries,
revisions, removal scripts and logs. Only SYSTEM and Administrators can write.
A pending revision prevents detection; retry that revision or explicitly remove it
before submitting another configuration. Removal verifies the managed configuration,
preserves drivers and shared ports, and keeps diagnostic files for administrator
review. No recursive endpoint cleanup is performed. Run removal before deleting
retained binaries, payloads or logs. Old unused ports after updates remain for
manual reviewed cleanup.

Provisioning is not a print test. Offline skips live identity verification and
requires a profile prevalidated by an administrator. Printing requires connectivity.
Drivers must already be registered or supplied through the supported pinned local
archive recipe. Arbitrary vendor installers and driver downloads are unsupported.

Full tutorial and pilot checklist: docs/intune-deployment.md in the source repo.
Windows/SYSTEM/Intune pilot validation is required before production deployment.
