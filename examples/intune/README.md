# Illustrative printer app

`accounting.ssb` uses a documentation-only address and invented driver/identity.
It is **not a hardware capture** — it was built directly from the JSON shape
below, not from `profile capture` or `copy`. The `captured` provenance value
illustrates the schema required for a real administrator-prevalidated profile.
Replace this sample with a real validated profile before running an endpoint
install.

Every saved printer is a bundle (`internal/bundle`, extension `.ssb`) — there
is no separate bare-JSON profile format. `accounting.ssb`'s `manifest.json`
entry carries exactly this profile document:

```json
{
  "version": 1,
  "target": "192.0.2.40",
  "printer_name": "Example — Accounting Copier",
  "driver_name": "Example OEM Driver (replace with registered name)",
  "evidence": {
    "ip": "192.0.2.40",
    "provenance": "captured",
    "http_title": "Example copier (illustrative placeholder, not a hardware capture)"
  }
}
```

The [Intune packaging guide](../../docs/intune-deployment.md) and the [README's
Intune section](../../README.md#intune-packaging) describe generating a complete
sample bundle with `spoolsmith intune build` or `intune wizard` (CLI or desktop
GUI). Select the
separately managed driver prerequisite for this example — it has no bundled
archive. Payloads and binaries are deliberately excluded from source control;
the generator uses the local CLI binary you select and calculates its SHA-256
for review automatically.

```powershell
spoolsmith intune build --profile examples\intune\accounting.ssb `
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
