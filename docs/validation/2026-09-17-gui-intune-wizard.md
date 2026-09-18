# GUI Intune wizard enablement — real Windows validation

Follow-up to the CLI-side `dddf4f1` ("Ship Intune packaging on the CLI") round.
This enables the desktop GUI's "Build an Intune printer app..." button, which had
been left disabled ("CLI only this round") pending its own pass. The wizard
dialog itself (`cmd/spoolsmith-gui/intune_windows.go`) was already fully
implemented; nothing in `internal/intune` changed — the GUI calls the same
`Prepare`/`Export` the CLI does, already proven on real Windows/SYSTEM in
`2026-09-15-windows11-results.md`. This record covers only the GUI plumbing.

## What changed

- `cmd/spoolsmith-gui/main_windows.go`: dropped the button's `Enabled: false`
  and rewrote its "Coming soon" label, which had conflated "not yet tested" with
  the separately-scoped tenant-automation question.
- `cmd/spoolsmith-gui/intune_windows.go`: added `Accessibility` names to the
  wizard's text fields (profile, binary, SHA-256 pin, deployment metadata,
  output folder, preview) so they're addressable by the existing FlaUI harness.
- `test/gui/SpoolSmithGui.Tests`: added `AppFixture.CliExePath` (a real CLI build
  is required — `Prepare` refuses a binary that isn't `cmd/spoolsmith`, so the
  GUI's own exe doesn't work here) and two new tests:
  `Intune_wizard_button_is_enabled_and_opens_dialog` and
  `Intune_wizard_exports_a_local_only_package` (the full happy path: fill
  profile/binary, check the driver-prerequisite box, calculate hash, advance,
  fill deployment metadata, validate/preview, export, dismiss the native
  confirmation, and assert the expected files land on disk).
- `.github/workflows/desktop-validation.yml` and `README.md`: build the CLI
  binary alongside the GUI one so the new test's default binary path resolves.

## Real Windows run

Ran on kubert (`DESKTOP-BBQQE4J`, Windows 11, 192.168.68.227) against fresh
`GOOS=windows GOARCH=amd64` builds of both `spoolsmith.exe` and
`spoolsmith-gui.exe` from this change. Launching the GUI over a plain SSH
command failed for every test, including the pre-existing
`MainWindow_launches_with_expected_title` — the app process started (confirmed
via `Get-Process`) but its window never attached to a UI-Automation-visible
desktop. OpenSSH-for-Windows spawns command processes in a non-interactive
window station even when a real console session is active, which is exactly
what `qwinsta` showed here (`kubert` logged into session 2, `console`, active).
Routing the same run through a Scheduled Task created with `/IT` (interactive,
tied to that logged-on session) instead of a raw SSH command fixed it — the app
rendered on the real desktop and UI Automation could see it.

```
Test Run Successful.
Total tests: 3
     Passed: 3
Total time: 50.7645 Seconds

Passed  Intune_wizard_button_is_enabled_and_opens_dialog   [15 s]
Passed  MainWindow_launches_with_expected_title            [2 s]
Passed  Intune_wizard_exports_a_local_only_package         [31 s]
```

The export test's assertions ran against real exported files on the Windows
filesystem (not just UI state): `install.ps1`, `uninstall.ps1`, `detect.ps1`,
`runtime.ps1`, `README.txt`, `profile.json`, `deployment.json` and
`spoolsmith.exe` were all present in the output folder, and `driver.exe` was
correctly absent for the driver-prerequisite path.

## Boundaries

- The wizard's "Browse profile..." / "Browse CLI..." native file-picker buttons
  were not exercised — the tests set the path fields directly via UI Automation
  rather than driving `GetOpenFileName`. A person should still click through
  those once before relying on them; this pass covers the field/validate/export
  pipeline, not the OS file dialogs.
- No Intune tenant or Company Portal interaction is in scope here, matching the
  CLI round — `intune build`/`wizard` (CLI or GUI) only ever writes local files.
- This is GUI plumbing validation on top of already-proven packaging logic; it
  does not re-run the CLI's own real-hardware Intune checks.
