# Desktop / CLI parity in v0.6.0

| Workflow | CLI | Desktop |
|---|---|---|
| Installed queues | `printers` | This PC; unsupported copy sources show a reason |
| Registered drivers | `drivers` | Add a printer → printer settings → Refresh drivers |
| Network discovery | `discover` | Add a printer → Scan; startup discovers the connected subnet |
| Known IP | `profile capture` / `install <ip>` | Use IP directly → Save and review / Review catalog setup |
| Copy a queue and optional driver | `copy`, `clone` | This PC → Copy to a file; optional driver and note |
| Apply a copied queue | `apply` | Add a printer → Open a printer file |
| Update from a bundle | `apply --update` | Review → More options → Update an existing queue |
| Inspect bundle integrity and manifest | `bundle inspect` | Tools → Inspect a `.ssb` file |
| Add / update / remove a saved setup | `add`, `configure`, `remove --profile` | Open a saved setup → Set up / Update to match / Remove |
| Edit saved settings | `profile edit` | Saved setups → Edit; preserves a backup |
| Local configuration check | `status --profile` | Saved setups → Check status |
| Bulk saved JSON transfer | `profile export-all`, `profile import-all` | Saved setups → Export all JSON / Import all JSON |
| Move an existing queue | `repoint` | This PC → Change address |
| Remove by queue name | `uninstall` | This PC → Remove printer |
| Offline provisioning | `--offline` | Review → More options → Do not contact the printer |
| Optional driver purge | `--purge-driver` | Removal review → More options |
| Catalog family override | `--force-family` | Catalog setup review → More options |
| Evidence and catalog | `inspect`, `catalog families`, `catalog probe` | Tools → Inspect / Catalog |
| Complete plan | `--dry-run --json` | Preview changes → Full plan / JSON |

Mutation previews run the shared Go workflow. Apply requires a native confirmation;
execution is bound to the reviewed plan and checks current state again. CLI unattended
flags remain CLI controls; the desktop always requests confirmation.

The blue header, lighter surfaces, Segoe UI type and revised spacing use the existing
native Windows toolkit and OS visual styles. The setup form replaces the discovery
area while editing, keeping its actions available at the minimum window size.

JSON collections contain all supported profile properties, including captured evidence
and driver-package references. They contain neither executable commands nor driver
archives. Import validates the full document and refuses existing filenames before
writing; it does not change Windows printer configuration. Carry archives separately
with the same relative directory layout. A collection is limited to 1,000 profiles and
16 MiB; individual profiles retain the 1 MiB limit.

Intune packaging is deliberately disabled in both shipped front ends until tenant
validation. A local configuration check does not verify reachability or physical output.
