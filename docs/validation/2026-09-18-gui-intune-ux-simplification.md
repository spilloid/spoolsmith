# Intune wizard UX simplification — real Windows validation

Follow-up to `2026-09-17-gui-intune-wizard.md`. That round enabled the GUI
Intune wizard as a straight port of the CLI's fields — three pages, all
manual entry (deployment ID, revision, SHA-256, output folder). After using
it successfully on real hardware, the operator asked for it to feel less
"unneeded technical": both the CLI and GUI now derive sane defaults from the
profile and auto-hash the CLI binary, and the GUI collapsed to two pages with
an optional advanced-settings panel. See `internal/intune/defaults.go`
(`ProfileDefaults`, `SuggestOutput`) and the updated `cmd/spoolsmith/intune.go`
/ `cmd/spoolsmith-gui/intune_windows.go`.

## Process

This round was implemented by Codex (`gpt-6-astra`, adversarial peer review
mode) in an isolated git worktree, then verified independently before any of
it reached `main`:

1. Astra implemented the simplification with dedicated tests for the
   trust-model invariants (binary identity/capability checks still run on an
   auto-computed hash, an auto-pinned payload still can't change after
   preview, `SuggestOutput` never creates or overwrites). Native and
   `GOOS=windows` builds/vet/tests were independently re-run and passed; the
   C# test project was independently compile-checked (podman
   `mcr.microsoft.com/dotnet/sdk:8.0`, `EnableWindowsTargeting=true`).
2. **First real Windows run: 28/30 passed.** Both Intune tests failed:
   - `Intune_wizard_button_is_enabled_and_opens_dialog`: `Find` threw "no
     control" for a collapsed advanced-settings field, rather than finding it
     offscreen.
   - `Intune_wizard_exports_a_local_only_package`: same, for the "Export
     reviewed package" button after navigating away from its page via a new
     "Back to settings" button.
   Neither is possible to catch without real UI Automation against a real
   desktop — this is exactly why the round wasn't merged on Astra's
   self-report alone.
3. Sent both real failures back to Astra (`codex exec resume`) with the exact
   TRX messages and stack traces. It traced `walk`'s HWND creation and cited
   [Microsoft's `IsOffscreen` documentation](https://learn.microsoft.com/en-us/dotnet/api/system.windows.automation.automationelement.automationelementinformation.isoffscreen)
   to establish the real root cause: UIA2 may omit a hidden native control's
   subtree entirely rather than reporting it offscreen — the dialog code was
   correct, the tests' assumption (borrowed from `AppFixture.SelectTab`'s
   older, overgeneralized comment, which it also corrected) was not. It
   rewrote the tests to poll for visibility either way, reselect the review
   page with a real click before querying its button (matching
   `AppFixture.SelectTab`'s established click-vs-`SelectionItemPattern`
   pattern), and strengthened the assertions — collapsed/expanded field
   values now must survive the round trip, and navigating back to review
   without re-validating must leave export disabled **and** must not write
   files.
4. **Second real Windows run: 29/30 passed.** One remaining failure: a
   startup race where `OpenIntuneWizard()`'s helper returned as soon as the
   dialog's window handle existed, before its first page's controls were
   reliably queryable — the one test that skipped straight to `SetText`
   without an intervening wait hit it. This was a test-harness timing gap,
   not a product or logic issue, so it was fixed directly rather than sent
   back: `OpenIntuneWizard()` now waits for `intune-profile` to be visible
   before returning, the same pattern already used everywhere else.
5. **Third real Windows run: 30/30 passed**, including the full 28-test
   pre-existing suite (no regressions) alongside both Intune tests.

## Environment

Same as the prior round: kubert (`DESKTOP-BBQQE4J`, Windows 11,
192.168.68.227). SSH-launched GUI processes still can't attach to a visible
desktop for UI Automation even with an active console session; all three runs
used an interactive Scheduled Task (`/IT`, tied to the logged-on session) as
established previously.

## Manual real-hardware use (informal, same session)

Separately from the automated suite, the operator used the (pre-UX-simplification)
wizard build on kubert to package and deploy a real Brother HL-L2315D: added
the printer with its pulled driver package (not a bare IP mapping), saved the
setup, deleted it, dropped the network, remapped, and queued a real print job
successfully. This predates the UX round above but is the reason the operator
is shipping this to their own daily work.

## Boundaries

Same as the prior round: no Intune tenant/Company Portal interaction, and the
wizard's native file-picker "Browse..." buttons still aren't exercised by the
automated suite (fields are set directly). Packaging remains local-only.
