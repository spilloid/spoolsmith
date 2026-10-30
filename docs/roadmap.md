# Remaining work for SpoolSmith

Updated October 30, 2026, for v1.5.3. What has shipped is in [`releases/`](../releases/);
the CLI/desktop split is in [gui-parity.md](gui-parity.md). What's left is mostly
validation on real hardware and hardening, then the future scope at the end.

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
3. **Broaden hardware coverage.** Copy and apply on more brands and drivers (Canon
   included), and record each one. Brother HL-L2315D evidence, plus the HP and Sharp
   operator report, doesn't establish compatibility for anything else.
4. **Check the native desktop on real displays.** *(partly done in v1.5.0.)* A FlaUI
   audit at 125% scaling (`AccessibilityTests`, `dist/gui-accessibility.txt`) found and fixed a
   real defect: Tab did not move focus in the main window; it now walks every control on every
   page. All interactive controls have accessible names and keyboard focus, but the names are
   internal ids rather than readable labels. Still open: 100%, 150% and 200% scaling, Windows
   high contrast, a real screen reader (which also settles how the results tables are
   announced), and readable accessible names. Hosted Windows automation does not establish
   those outcomes. Extend end-to-end coverage through actual GUI copy/apply and saved-setup transfer.
5. **Validate the automatic offline fallback (2026-09-22, see CLAUDE.md) against
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

7. **Run v1.5.1's elevated hardware checklist.** The hardening pass was tested without
   elevation and without touching a real driver, queue, port or spooler. Still to run: a
   stopped spooler, a same-name conflict, a forced step-2 failure, the mapped-drive shield
   against a real share, a non-English Windows display language, and the FlaUI desktop
   suite. See [the v1.5.1 notes](../releases/v1.5.1.md#what-is-not-verified).

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
- **Keep long local operations responsive.** *(v1.5.0.)* Desktop local
  status and printer-file verification now run off the UI thread with a time bound
  (status) and a title-bar "checking" notice (file), and a result that arrives after
  the dialog closed is dropped. Still open: a Cancel button and a progress bar for
  very large driver bundles, and exercising close/cancel on real hardware.
- **Improve library portability.** *(v1.5.0.)* The saved-setups folder is
  now the one the operator last chose (remembered per user in
  `%APPDATA%\SpoolSmith\library-folder.txt`), else `profiles` beside the app when
  that is writable, else `%LOCALAPPDATA%\SpoolSmith\profiles` (the machine-wide MSI
  under Program Files). `SPOOLSMITH_PROFILES_DIR` still overrides all of these.
  Still open: clearer recovery when a referenced driver archive is missing.
- **Interrupted operations: guidance shipped, rollback not.** *(v1.5.0.)*
  A failed step now says earlier steps may already have been applied and that
  adding the same printer again reuses what exists; a native-driver switch that could not be restored is drawn
  as a failure with the queue's state, not a quiet skip. Nothing is rolled back
  automatically.
- **Still open from the v1.5.1 review.** Spooler calls have no timeout outside the Intune
  wrapper, and exit code 1 doesn't distinguish "nothing changed" from "partly changed".
  *(v1.5.3 closed the other two: the plan now names every TrustedPublisher signer it may
  add, and driver files stage only in an administrators-only folder. Uninstall still leaves
  that publisher trust in place.)*
- **Update delivery.** Releases ship a portable ZIP and a signed, machine-wide MSI
  (from v1.3.0; see `installer/README.md`) with checksums and Authenticode-signed
  executables. A newer MSI upgrades an older one when it is installed, but nothing
  checks for or delivers updates: there is no auto-updater, and deployment tools or the
  operator decide when to move to a new version. Driver signature enforcement is a
  separate mechanism.

## Future feature scope

Not implemented: shared (`\\server\queue`) connections, LPR, IPPS-only printers,
non-9100 RAW ports, multicast discovery and macOS/Linux. Removing the catalog-driven
commands is tracked in [#22](https://github.com/spilloid/spoolsmith/issues/22).
SpoolSmith fetching drivers itself would need its own source and trust policy; today
only Windows downloads anything, during IPP setup. None of this blocks the shipped
RAW 9100, WSD, IPP and USB workflows.

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
