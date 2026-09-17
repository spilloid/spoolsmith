# Remaining work after v0.6.0

Updated September 17, 2026. The current release includes the native desktop's
copy/apply, direct-IP, discovery, profile editing, status, offline and bulk JSON
workflows. The workspace TODO requests for desktop parity and JSON transfer are
implemented. Remaining work is primarily broader validation and product hardening,
with additional protocols as future scope.

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
4. **Check the native desktop on real displays.** Exercise 125%, 150% and 200%
   scaling, keyboard-only navigation, a screen reader and Windows high contrast.
   Hosted Windows automation covers navigation, minimum-size layout and selected
   workflows; it does not establish those accessibility or hardware outcomes.
   Extend end-to-end coverage through actual GUI copy/apply and JSON transfer.

## Known behavior to harden

- **Driver purge may conservatively retain an unused driver.** Windows queue
  removal is asynchronous; the following in-use check can still see the deleted
  queue. A bounded recheck must preserve shared-driver protection and avoid an
  automatic spooler restart that disrupts unrelated printing. Observed in v0.6.0.
- **Interrupted operations are not transactions.** A failure after port creation
  can leave an unused port; retry safely reuses it. Validate interruption/retry
  and make residual resources and recovery guidance clearer. Repoint intentionally
  retains the old port.
- **Keep long local operations responsive.** Local status and bundle verification
  can currently run synchronously in desktop dialogs. Move slow work off the UI
  thread with cancellation and explicit progress, then exercise closing/cancelling.
- **Improve library portability.** The default profile folder is beside the app;
  a writable extraction folder is needed. Folder selection is per session.
  Consider a persistent per-user location and clearer missing-archive recovery.
- **Distribution polish.** Evaluate signed executables, an installer and update
  delivery. Today's release is a portable Windows x64 ZIP with checksums; application
  executables are unsigned. Driver signature enforcement is a separate mechanism.

## Future feature scope

USB/shared-queue setup, IPP/LPR strategies, multicast discovery, broader catalog
recipes and macOS/Linux support are not implemented. Automatic OEM driver downloads
would require a separately designed source/trust policy. These are expansion work,
not conditions for using the supported RAW TCP 9100 workflow.

## Intune remains held

The internal implementation has partial native Windows/SYSTEM evidence, but no
real tenant/Company Portal pilot. Entry points remain disabled. See the
[actual pilot results](validation/2026-09-15-windows11-results.md) for unrun lifecycle
cases and the [unreleased guide](intune-deployment.md) for the planned workflow.
Rebuilding source alone does not enable the feature.

## Post-release fixes on main

These follow v0.6.0 and are not in its existing ZIP:

- Invalid saved profiles cannot enter the desktop editor and lose unrecognized data.
- Bulk JSON export refuses the profile folder itself, including directory aliases,
  so a collection cannot be mistaken for an individual profile on the next export.
- Empty library guidance and initial selection after switching folders are consistent.
- The site leads with the downloadable app, desktop instructions and file-type guidance.
  Documentation distinguishes one-PC tests from two-PC validation and released
  workflows from held Intune work.

See the [quality review](validation/2026-09-17-quality-review.md) and the
[follow-up VM hardware QC](validation/2026-09-17-vm-hardware-qc.md) for checks and evidence.
