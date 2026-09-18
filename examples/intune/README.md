# Illustrative printer app

`accounting.json` uses a documentation-only address and invented driver/identity.
It is **not a hardware capture**. The `captured` provenance value illustrates the
schema required for a real administrator-prevalidated profile. Replace this sample
with a real validated profile before running an endpoint install.

The [Intune packaging guide](../../docs/intune-deployment.md) and the [README's
Intune section](../../README.md#intune-packaging) describe generating a complete
sample bundle with `spoolsmith intune build` or `intune wizard` (CLI or desktop
GUI). Select the
separately managed driver prerequisite for this example — it has no bundled
archive. Payloads and binaries are deliberately excluded from source control;
the generator uses the local CLI binary you select and calculates its SHA-256
for review automatically.

```powershell
spoolsmith intune build --profile examples\intune\accounting.json `
  --binary spoolsmith.exe --driver-prerequisite --dry-run
```

Review the manifest and suggested destination, then repeat without `--dry-run`
to export. The app name defaults to **Example — Accounting Copier**, with a
queue-derived deployment ID, revision 1 and a description containing the name
and address. An unused folder is suggested beside the profile. Use `--output`
for another destination or `--binary-sha256` for an independently approved pin.
The CLI wizard and two-page GUI offer the same defaults and require a separate
export confirmation after preview. Advanced settings allow metadata, revision
and policy overrides. Reuse the existing ID and increase the revision for updates;
folder suffixes on repeated exports do not change the revision.
