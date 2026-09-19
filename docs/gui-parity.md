# Desktop / CLI parity

Reconciled for v0.7.4. Where CLI and desktop genuinely differ rather than just
using different words for the same thing, that's called out under the table instead
of glossed over.

| Workflow | CLI | Desktop |
|---|---|---|
| Installed queues | `printers` (JSON on stdout, human table on stderr; `--json` suppresses the table) | This PC; unsupported copy sources show a reason |
| Registered drivers | `drivers` | Add a printer → printer settings → Refresh drivers |
| Network discovery | `discover` | Add a printer → Scan; startup discovers the connected subnet |
| Known IP | `profile capture` / `install <ip>` | Use IP directly → Save and review / Use catalog identification instead... |
| Copy one queue and optional driver | `copy`, `clone` (driver opt-in: `--include-driver`) | This PC → Copy to a file (driver opt-out: on by default) |
| Copy every copyable queue at once | `copy --all [<output-dir>] [--include-driver]` | This PC → Copy all printers; driver checkbox, progress, stop, per-queue results and copyable JSON |
| Apply a copied queue | `apply` | Add a printer → Open a copied printer (.ssb)... |
| Update from a bundle | `apply --update` | Review → More options → Update an existing queue |
| Inspect bundle integrity and manifest | `bundle inspect <file>` | Tools → Inspect (also takes a `.ssb` path directly) |
| Add / update / remove a saved setup | `add`, `configure`, `remove --profile` | Open a saved setup → Set up / Update to match / Remove |
| Edit saved settings | `profile edit` | Saved setups → Edit; preserves a backup |
| Local configuration check | `status --profile` (JSON on stdout plus a one-line stderr summary) | Saved setups → Check status |
| Bulk saved JSON transfer | `profile export-all`, `profile import-all`; `--dry-run` reviews files and conflicts | Saved setups → Export all JSON / Import all JSON → destination and file review |
| Move an existing queue | `repoint` | This PC → Change address |
| Remove by queue name | `uninstall` | This PC → Remove printer |
| Offline setup | `--offline` | Review → More options → Offline setup |
| Optional driver purge | `--purge-driver` | Removal review → More options |
| Catalog family override | `--force-family` | Catalog setup review → More options |
| Evidence and catalog | `inspect`, `catalog families`, `catalog probe` | Tools → Inspect / Catalog |
| Complete plan and execution result | `--dry-run --json` / execution JSON | Preview changes / completed execution → Full plan / JSON → Copy |

Mutation previews run the shared Go workflow. Apply requires a native confirmation;
execution is bound to the reviewed plan and checks current state again. CLI unattended
flags remain CLI controls; the desktop always requests confirmation.

Bulk copy uses the same `bundle.CreateAll` in both surfaces: one `.ssb` per supported
queue, no overwrites, independent outcomes, and early filename checks before probing
or exporting drivers. The GUI retains its existing include-driver default; the CLI
requires `--include-driver`. Both expose the same `requested`, `written`, `skipped`,
`failed` and per-queue JSON result. For compatibility, the CLI exits successfully if
at least one file was written, even when other queues failed; inspect the counters
for partial success. Cancellation exits nonzero and preserves completed files.
Applying a folder of bundles as one batch remains unimplemented: each bundle uses
the existing per-printer review and confirmation.

The blue header, lighter surfaces, Segoe UI type and revised spacing use the existing
native Windows toolkit and OS visual styles. The setup form replaces the discovery
area while editing, keeping its actions available at the minimum window size.

JSON collections contain all supported profile properties, including captured evidence
and driver-package references. They contain neither executable commands nor driver
archives. Transfer previews show filenames, printer settings, archive references and
all destination conflicts. The desktop executes the collection it reviewed; a source
file changed after review cannot silently change the import. Import rechecks existing
filenames before writing; it does not change Windows printer configuration. Carry archives separately
with the same relative directory layout. A collection is limited to 1,000 profiles and
16 MiB; individual profiles retain the 1 MiB limit.

Intune packaging (local-only Win32 app export) is available from both the CLI
and the desktop GUI; see [Intune packaging](../README.md#intune-packaging). Both
wizards produce the same source folder and README.txt naming the manual
`IntuneWinAppUtil.exe` command; only the CLI's `intune build` exposes
`--content-prep-tool`/`--content-prep-output` to run that packaging step itself
rather than leaving it as the documented manual command. A local configuration
check does not verify reachability or physical output.
