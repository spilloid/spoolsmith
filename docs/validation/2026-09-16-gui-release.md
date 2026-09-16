# v0.6.0 desktop release validation

The release continues Claude's uncommitted four-tab GUI work and includes the two
workspace TODO requests: bulk JSON transfer and a refreshed native appearance.

## Checks

- Native Linux `go build ./...`, `go vet ./...`, `go test ./... -count=1` passed.
- Windows cross-build and vet passed locally.
- GitHub CI passed on both Ubuntu and Windows, including native Windows PowerShell
  tests and compilation of the FlaUI desktop suite.
- The manual Desktop validation workflow runs the real executable with FlaUI and
  collects TRX results and real screenshots. It uses a hosted Windows desktop,
  fixture profiles, and loopback; it never confirms a Windows printer mutation.
- New core tests cover full-property collection round trips, filename collisions,
  traversal/reserved filenames, malformed data and invalid profiles. CLI coverage
  exercises export followed by import. Address-change execution rejects changed
  drivers and original ports after preview.

## Findings during GUI validation

1. At minimum size, the expanded setup form put Save and review below the window.
   Discovery now collapses while settings are edited; Back to discovery restores it.
2. Reattaching the main tab subtree had moved it after the footer in layout order.
   Its original position is now preserved, with a full-width blue header.
3. The initial tests searched only top-level windows for an owned modal dialog.
   They now also inspect the main window's modal windows.
4. The review handoff tests waited for a static accessibility label name, but the
   label exposes its updated sentence. The failure screenshot showed the correct
   selected review tab, printer name and action. The tests now wait for the stable
   output control and assert the actual summary text and disabled action button.
5. Dialog subtrees receive explicit control-ID attachment, matching the main
   window's existing workaround for nested native controls.

The first run passed 19/26 checks; the second passed 23/26. The third passed 24/27,
including direct-IP layout, dialog close, and an offline profile preview that
reports the absent driver while leaving Apply disabled. Its three remaining
failures were the handoff test lookup described above, not a failure to navigate.

The final [Desktop validation run](https://github.com/spilloid/spoolsmith/actions/runs/35102287642)
passed **27/27** checks on commit `fd24fa4`. This includes every saved-setup handoff,
empty-review gating, modal close, direct-IP setup, minimum/default-size captions,
fixture inspection, catalog, loopback discovery and offline missing-driver refusal.
[Linux and Windows CI](https://github.com/spilloid/spoolsmith/actions/runs/35102287763)
also passed for that commit. Subsequent release-record changes are documentation only.

## Boundaries

These checks do not constitute a new physical-printer pilot. The previous v0.5.0
Brother copy/staging evidence remains in `2026-09-15-copy-workflow.md`. No physical
print or live address-change mutation was performed for v0.6.0. No Intune tenant
was used, and Intune remains held out of the shipped surfaces.

The operator's Windows VM was reachable, but rejected SSH public-key authentication;
the hosted desktop workflow supplied the UI evidence for this release. Screenshots
on the product site come from that workflow and contain only the hosted runner's
Microsoft Print to PDF inventory, empty discovery and read-only screens.
