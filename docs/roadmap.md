# Remaining work for SpoolSmith

Updated September 24, 2026. v1.3.0 rebuilt the desktop around copying a printer off one PC and double-clicking it onto the next (see `docs/development/v1.3-gui-spec.md`). The v1.1.0 release makes `.ssb` the one printer file
and a plain `.zip` of `.ssb` files the one multi-printer set (`copy --all`, `apply`,
saved-setup export/import), includes drivers by default wherever they can be
exported, adds automatic offline fallback, replaces the desktop's tab strips with
sidebar navigation and refreshes the product site. Shipped releases now include the native desktop's
copy/apply, direct-IP, discovery, profile editing, status, offline and bulk JSON
workflows (v0.6.0); Intune packaging on both the CLI and desktop (v0.7.0–v0.7.1);
a UX pass plus bulk driver export via `copy --all` on the CLI (v0.7.3); and
`copy --all` on the desktop GUI plus a `--dry-run`/review preview for bulk
saved-setup transfer on both surfaces (v0.7.4) — see "What shipped in v0.7.4"
below and `docs/gui-parity.md` for the current CLI/desktop split. Remaining
work below is primarily broader validation and
product hardening, with additional protocols as future scope.

## Next validation priorities (outside Intune)

1. **Use two actual PCs.** Copy a supported queue and its driver on the source;
   apply on a second PC with neither registered driver nor driver-store package.
   Confirm the physical print, repeat apply, then remove only the test queue.
   The existing [copy test](validation/2026-09-15-copy-workflow.md) simulated an
   absent-driver destination by deleting the driver on the same PC. It did not
   print through the bundle-staged driver.
2. **Exercise a live address change.** Verify `repoint` with an actual reachable
   replacement address, physical output and retained old-port behavior. Only the
   preview has hardware evidence. Preserve unrelated queues and shared ports.
3. **Broaden hardware coverage.** Validate the HP LaserJet Pro M4xx family and
   additional explicitly configured drivers. Brother HL-L2315D evidence does not
   establish compatibility for every member of a catalog family.
4. **Check the native desktop on real displays.** *(partly done, v1.5 unreleased.)* A FlaUI
   audit at 125% scaling (`AccessibilityTests`, `dist/gui-accessibility.txt`) found and fixed a
   real defect: Tab did not move focus in the main window; it now walks every control on every
   page. All interactive controls have accessible names and keyboard focus, but the names are
   internal ids rather than readable labels. Still open: 100%, 150% and 200% scaling, Windows
   high contrast, a real screen reader (which also settles how the results tables are
   announced), and readable accessible names. Hosted Windows automation does not establish
   those outcomes. Extend end-to-end coverage through actual GUI copy/apply and saved-setup transfer.5. **Validate the automatic offline fallback (2026-09-22, see CLAUDE.md) against
   a real printer.** Power off (or unplug) a printer mid-copy and mid-apply on
   actual hardware, on both the CLI and desktop, and confirm: `copy` still
   writes a usable, clearly-marked bundle; `apply`/`install --profile` still
   falls back to offline and completes with a correct plan; the operator-visible
   notices are legible in the GUI transcript, not just on CLI stderr; and
   powering the printer back on and re-running `status`/`check-status` reports
   the mismatch (or match) correctly. Only unit tests with fake environments
   exist today.
6. **Validate printer sets and default driver export on real hardware.** As of
   2026-09-23 (see CLAUDE.md), `copy --all` writes one `.zip` set, `apply <set.zip>`
   walks each printer with its own plan and confirmation, saved-setup export/import
   carries embedded drivers, and every copy tries to include its driver. Go tests
   cover the set format, `copy --all`, per-member apply and the transfers; nothing
   of this has run against real printers or between two PCs yet, and the desktop
   suite and screenshots still need a re-run for the new set dialogs. Exercise a
   multi-printer set with drivers from an elevated source, a non-elevated copy
   (settings-only with its reason), an Explorer-made zip, and a cancelled
   `copy --all` (no set written).

## Unreleased: hardening pass (October 2026)

Fixes from an adversarial pass over failure paths and untrusted input; see the
2026-10-19 entry in [the dev log](development/dev-process.md). Not in any release yet.

- **PowerShell quoting closed for U+201E.** PowerShell treats the low double quote
  `„` as a string delimiter. A printer or driver name containing it, in a `.ssb`
  someone emailed, could end the quoted value and run its own PowerShell during the
  preview's driver check, before any confirmation and elevated if the session was.
  Every PowerShell quote character is now escaped, and a test runs hostile names
  through the real PowerShell tokenizer.
- **A failed step says what failed and what already changed.** The one-line error used to
  be `install: run "<the whole generated PowerShell command>": exit status 1`, so the
  desktop dialog, `--json`'s `error` field and the desktop action log lost the reason
  Windows gave. It is now, for example, `install: step 2 of 2 failed: The spooler service
  is not reachable. (exit status 1). Already done before it: step 1: Created port`. The
  same applies to removal and repoint. Full commands and output stay in the result's
  `ran` list. The reason comes from a `SPOOLSMITH-REASON:` line each generated command's
  error handler prints, not from PowerShell's error layout, so it is found on Windows in
  any display language (tested with German and French layouts, and through real
  PowerShell). The desktop's "step failed part-way" guidance is now checked first, so a
  Windows reason containing "already exists" no longer gets the "file already exists" lead.
- **The preview predicts the conflicts the apply would stop on.** A TCP/IP plan's own
  commands refuse a same-named queue with another driver or port (without `--update`) and a
  `RAW9100-<ip>` port that is not RAW 9100 at that address, but only at mutation time:
  `--dry-run` said "Preview complete" and the real apply then failed after confirmation.
  Preflight now reads the same inventory, read-only, and fails the preview and the apply
  the same way (exit 1) before anything is confirmed. IPP and USB plans are unchanged.
- **The CLI's action-log entry says why a run failed.** The desktop always recorded its
  error text; the CLI recorded only `"status":"error","exit_code":1`. The CLI now records the
  `spoolsmith <command>: ...` error lines it wrote to stderr. The log's location is now in
  the Safety and limits guide.
- **Shield button from a mapped drive.** The elevated relaunch handed over the printer
  file's path verbatim, so a file opened from `Z:` was "not found" in the elevated window
  (UAC's elevated token does not see the session's drive mappings), after the original
  window had closed. Mapped-drive paths are now handed over as UNC. A long network path no
  longer defeats this (the lookup retries with the buffer size Windows asks for), and when
  Windows cannot give the network path at all, the desktop says so and asks before
  relaunching instead of handing over `Z:\...` silently. Unit-tested with a fake resolver
  and a fake undersized call; the real `WNetGetUniversalName` call still needs a run
  against a real share.
- **A stopped spooler reads as a stopped spooler.** The preflight driver check reported a
  1,000-character echo of its own script and error record, followed by "driver not found
  — install it via Windows Update", and the desktop led with "This driver is not
  installed". Removal's queue lookup discarded the output and said only "exit status 1".
  Both now quote Windows' reason, and the desktop says to check the Print Spooler service.

## Known behavior to harden

- **Drivers from another PC are trusted as that PC is.** Copies now carry drivers
  by default. Payload hashes detect corruption and edits; Windows' catalog
  signature check at staging is the real gate. `--settings-only` remains the
  opt-out for sites that manage drivers separately.
- **Driver purge may conservatively retain an unused driver.** Windows queue
  removal is asynchronous; the following in-use check can still see the deleted
  queue. A bounded recheck must preserve shared-driver protection and avoid an
  automatic spooler restart that disrupts unrelated printing. Observed in v0.6.0.
- **Interrupted operations are not transactions.** A failure after port creation
  can leave an unused port; retry safely reuses it. Validate interruption/retry
  and make residual resources and recovery guidance clearer. Repoint intentionally
  retains the old port.
- **Keep long local operations responsive.** *(v1.5, unreleased.)* Desktop local
  status and printer-file verification now run off the UI thread with a time bound
  (status) and a title-bar "checking" notice (file), and a result that arrives after
  the dialog closed is dropped. Still open: a Cancel button and a progress bar for
  very large driver bundles, and exercising close/cancel on real hardware.
- **Improve library portability.** *(v1.5, unreleased.)* The saved-setups folder is
  now the one the operator last chose (remembered per user in
  `%APPDATA%\SpoolSmith\library-folder.txt`), else `profiles` beside the app when
  that is writable, else `%LOCALAPPDATA%\SpoolSmith\profiles` (the machine-wide MSI
  under Program Files). `SPOOLSMITH_PROFILES_DIR` still overrides all of these.
  Still open: clearer recovery when a referenced driver archive is missing.
- **Interrupted operations: guidance shipped, rollback not.** *(v1.5, unreleased.)*
  A failed step now says earlier steps may already have been applied and that
  adding the same printer again reuses what exists; a native-driver switch that could not be restored is drawn
  as a failure with the queue's state, not a quiet skip. Nothing is rolled back
  automatically.
- **Update delivery.** Releases ship a portable ZIP and a signed, machine-wide MSI
  (from v1.3.0; see `installer/README.md`) with checksums and Authenticode-signed
  executables. A newer MSI upgrades an older one when it is installed, but nothing
  checks for or delivers updates: there is no auto-updater, and deployment tools or the
  operator decide when to move to a new version. Driver signature enforcement is a
  separate mechanism.

## Future feature scope

USB/shared-queue setup, IPP/LPR strategies, multicast discovery, broader catalog
recipes and macOS/Linux support are not implemented. Automatic OEM driver downloads
would require a separately designed source/trust policy. These are expansion work,
not conditions for using the supported RAW TCP 9100 workflow.

## Intune: packaging is supported; tenant automation is not planned

`spoolsmith intune build`/`wizard` is a supported CLI command, and the desktop
GUI offers the same wizard (sidebar → Intune package → "Build an Intune printer app..."). Both
export a reviewable Win32 app package with the exact install/uninstall commands
and detection rule, entirely locally — no tenant sign-in, upload, group creation
or assignment. Uploading and assigning the package in Intune stays a manual
step; see [the packaging guide](intune-deployment.md). GUI validation:
[2026-09-17-gui-intune-wizard.md](validation/2026-09-17-gui-intune-wizard.md).

Deliberately **not** planned: signing in to an M365/Intune tenant and
automatically uploading, creating groups, or assigning the app. SpoolSmith is a
printer deployment enabler, not a tenant-management tool — that scope stays out
unless a future decision says otherwise.

Still open: a real tenant/Company Portal pilot (see the [unrun lifecycle
cases](validation/2026-09-15-windows11-results.md)) validating the manual upload
path above, which isn't required to use packaging today.

## What shipped in v0.7.4

- Bulk driver export (`copy --all`) is now also available on the desktop GUI
  (This PC → Copy all printers...), sharing the same `bundle.CreateAll` batch
  implementation the CLI uses: one `.ssb` per queue, independent per-queue
  outcomes, early filename-collision checks before any probe or driver export,
  and a Stop control that keeps completed files.
- `profile export-all`/`profile import-all` gained a `--dry-run` preview (CLI)
  and a destination-review step (desktop) that lists filenames, printer
  settings and destination conflicts before anything is written.
- A single `copy`'s destination filename is now checked before the network
  probe and any driver export, matching the batch path's existing preflight.
- The desktop's saved-setup export/import review dialog can now be closed at
  any time, including mid-check or mid-save, instead of blocking indefinitely
  on a slow or unresponsive destination.

See [gui-parity.md](gui-parity.md) for what's shared between the CLI and
desktop today, and the [quality review](validation/2026-09-17-quality-review.md)
for the last full pass before this round of changes.
