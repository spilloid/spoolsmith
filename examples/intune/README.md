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
the generator produces only the scripts and manifest from a CLI binary and
SHA-256 you supply.
