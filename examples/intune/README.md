# Illustrative printer app (unreleased)

`accounting.json` uses a documentation-only address and invented driver/identity.
It is **not a hardware capture**. The `captured` provenance value illustrates the
schema required for a real administrator-prevalidated profile. Replace this sample
with a real validated profile before running an endpoint install.

The [Intune validation guide](../../docs/intune-deployment.md) describes generating
a complete sample bundle from an explicitly enabled pilot build. Intune entrypoints
are disabled in released binaries and current source; rebuilding alone does not enable them.
A pilot uses a compatible Windows CLI and its reviewed SHA-256. Select the separately managed
driver prerequisite for this example. Payloads and binaries are deliberately
excluded from source control; the generator produces the scripts and manifest.
