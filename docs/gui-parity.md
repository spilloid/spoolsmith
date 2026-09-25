# Desktop / CLI parity

Reconciled for v1.3.0. Where CLI and desktop genuinely differ rather than just
using different words for the same thing, that's called out under the table instead
of glossed over.

The desktop sidebar has two pages, **This PC** and **Add a printer**. Intune
package, Inspect, Action log and Saved setups are under **More**
at the foot of the sidebar. Every change to Windows is reviewed on the **apply
sheet**, which opens whenever there is something to apply: a printer file
(double-clicked, dropped, pasted or opened), a scanned or saved printer, or
Remove / Change address on This PC.

| Workflow | CLI | Desktop |
|---|---|---|
| Installed queues | `printers` (JSON on stdout, human table on stderr; `--json` suppresses the table) | This PC table; printers SpoolSmith can't copy are greyed with their reason |
| Registered drivers | `drivers` | Add a printer → printer settings → Refresh drivers |
| Network discovery | `discover` | Add a printer lists the connected subnet's printers on its own; Scan a different network or IP... |
| Known IP | `profile capture` (`install <ip>` is pending deprecation) | Scan a different network or IP → Use IP directly → Save and review |
| Copy one queue, driver where possible | `copy`, `clone` (driver included when possible; `--settings-only` opts out) | This PC → select → Copy 1 printer... (driver checkbox on by default; settings only, with the reason, when it can't be exported); or Ctrl+C to put the .ssb on the clipboard |
| Copy several queues into one printer set | `copy --all [<printers.zip>] [--settings-only]` (every copyable queue) | This PC → select several (Ctrl+A for all) → Copy N printers...: one set (.zip), progress, stop, per-printer results and Copy results; or Ctrl+C for individual .ssb files on the clipboard |
| Apply a copied printer | `apply <file.ssb>` | Double-click the .ssb (once the association is set up), drop or paste it on the window, `spoolsmith-gui.exe <file.ssb>`, or Add a printer → Open a printer file... |
| Apply a printer set | `apply <set.zip>` (each printer its own plan and confirmation; `--member` picks one) | Open, drop or paste the set → printer list → review each printer on its own sheet |
| Update from a bundle | `apply --update` | Sheet → More options → Update an existing queue |
| Inspect bundle integrity and manifest | `bundle inspect <file.ssb\|set.zip>` (lists a set's printers) | More → Inspect takes an IP, a `.ssb` or a set `.zip` (lists the set's printers) |
| Add / update / remove a saved setup | `add`, `configure`, `remove --profile` | More → Saved setups → Set up / Update to match / Remove |
| Edit saved settings | `profile edit` | Saved setups → Edit; preserves a backup |
| Local configuration check | `status --profile` (JSON on stdout plus a one-line stderr summary) | Saved setups → Check status |
| Bulk saved-setup transfer | `profile export-all <folder> <set.zip>`, `profile import-all <set.zip> <folder>`; `--dry-run` reviews files and conflicts | Saved setups → Export all / Import all (.zip) → destination and file review |
| Move an existing queue | `repoint` | This PC → Change address |
| Remove by queue name | `uninstall` | This PC → Remove printer |
| Offline setup | Automatic fallback for saved printers; `--offline` skips probing | Automatic fallback; sheet → More options → Offline setup skips probing |
| Optional driver purge | `--purge-driver` | Removal sheet → More options |
| Catalog family override | `--force-family` (pending deprecation) | Not offered |
| Evidence and catalog | `inspect`; `catalog families`, `catalog probe` (pending deprecation) | More → Inspect |
| Intune packaging | `intune build`, `intune wizard` | More → Intune package → Build an Intune printer app... (two-step wizard) |
| Complete plan and execution result | `--dry-run --json` / execution JSON | Sheet → Details (transcript) → Full plan and result → Copy |
| Notes for a ticket | execution JSON | Finished sheet → Copy notes for the ticket |

Mutation previews run the shared Go workflow; the sheet prepares its preview as
soon as it opens and shows the plan as a list of steps. Apply requires a native
confirmation; execution is bound to the reviewed plan and checks current state
again. CLI unattended flags remain CLI controls; the desktop always requests
confirmation.

**Elevation differs on purpose.** The CLI fails closed when not run as
Administrator. The desktop's main button carries the UAC shield instead
(*Install as administrator...*): pressing it asks Windows for consent and
relaunches SpoolSmith elevated on the same printer, which prepares the preview
again and asks for the same single confirmation.

Bulk copy uses the same `bundle.CreateAll` in both surfaces: one printer set (`.zip`)
holding one `.ssb` per supported queue, duplicate member names suffixed `-2`, `-3`, an
existing set file refused rather than overwritten, and independent per-printer outcomes.
Both surfaces include each driver where possible and report why one wasn't; the CLI's
`--settings-only` and the desktop's cleared checkbox leave every driver out. The CLI
writes the `requested`, `written`, `skipped`, `failed` and per-queue JSON result to
stdout; the desktop's **Copy results** copies a plain-text report of the same outcome.
For compatibility, the CLI exits successfully if at least one printer was saved, even
when other queues failed; inspect the counters for partial success. Cancellation saves
no set on either surface. Applying a set never batches confirmations: the CLI walks
its printers one plan and one confirmation at a time, and the desktop lists them so
you review and confirm each one.

The blue header, lighter surfaces, Segoe UI type and revised spacing use the existing
native Windows toolkit and OS visual styles. A sidebar replaces the former top tab
strip and the nested Tools tabs, and SpoolSmith's own dialogs carry the same blue
header band; message boxes and file pickers remain standard Windows. The main
window's minimum size is 960×640 (default 1060×720). The setup form replaces the discovery
area while editing, keeping its actions available at the minimum window size.

Saved-setup sets are the same printer sets: a plain `.zip` of the member `.ssb` files,
byte for byte, including captured evidence, driver-package references and any embedded
driver. Vendor archives named by a `driver_package` reference travel separately.
Transfer previews show filenames, printer settings, whether each carries a driver,
archive references and all destination conflicts. Both surfaces pin the transfer to
what was reviewed: a printer file or set that changed after review is refused. Import
rechecks existing filenames before writing; it does not change Windows printer
configuration. Carry archives separately with the same relative directory layout. A set
holds at most 1,000 printers and 8 GiB uncompressed; each printer file keeps its own
1 GiB driver-payload limit.

Intune packaging (local-only Win32 app export) is available from both the CLI
and the desktop GUI; see [Intune packaging](../README.md#intune-packaging). All
three entry points (`intune build`, `intune wizard`, the desktop wizard) produce
the same source folder and README.txt, and all three also create the
`.intunewin` with Microsoft's `IntuneWinAppUtil.exe` when it sits beside the
executable, defaulting the output to `<export folder>-intunewin`. Overrides differ
only in spelling: `--content-prep-tool`/`--content-prep-output`/`--no-content-prep`
on `build`, the advanced questions in `wizard`, and the tool and output fields
on the desktop review page. (Through v1.0.0 the desktop wizard and `wizard` had
no way to run the tool at all; only `build` did, and only with both flags.) A
local configuration check does not verify reachability or physical output.
