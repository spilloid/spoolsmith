### D-0050 — Operator direction: retain Windows-managed setup by IP in SpoolSmith v1.4

**Date:** 2026-10-01

**Decision:** During elevated v1.4 hardware verification, the operator asked whether
SpoolSmith could set up a printer by IP and let Windows obtain/select its driver,
then explicitly directed keeping that capability in v1.4's feature stack. The
operator subsequently directed adherence to corporate-strategy in the adjacent
repository. Scope is Windows-managed discovery/class-driver selection, alongside
existing explicit installed-driver mapping, USB copies, and WSD conversion.
Arbitrary automatic OEM driver retrieval is not claimed as implemented.

**Preserved boundaries:** Exactly one confirmation of the reviewed plan; elevation
for local mutation; Windows owns inbox/Windows Update driver acquisition and trust;
SpoolSmith does not independently fetch and execute installers. Existing
conflicting queues are not implicitly removed or repointed. The existing
NetViz read-only boundary and local-only Intune packaging remain in force.

**Bar judgment:** No board review was invoked. This is direct operator direction
within the Windows-owned mechanism anticipated by D-0040 and daily-use mapping
of D-0041, retaining their trust and confirmation model. No new target customer,
commercial/product-status grant, paid commitment, or universal-driver promise
is asserted. A future independent OEM download mechanism remains a separate
decision. No board vote or waiver is inferred from this direction.

**Evidence/status:** Elevated OEM export, RAW queue creation/reapplication, and
USB preparation using an installed driver passed. Machine preparation cleared
an IPP duplicate-device collision. Windows created an IPP connection exposed
as a WSD-named port; endpoint verification is being corrected and validated.
Source queue restoration required recreating its device port and preserving
the original driver/settings. Clean-target setup, cross-PC apply, and physical
printing remain unverified. Implementation and final hardware verification are
in progress; v1.4 is a draft, not a published release. See SpoolSmith's
`docs/v1.4-windows-driver-spec.md` and
`docs/validation/2026-10-01-wsd-conversion.md` for the contract and actual results.

**Affected products:** SpoolSmith only. No simulated labor allocation or advance
was requested or recorded.
