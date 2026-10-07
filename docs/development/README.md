# Engineering record

This folder holds SpoolSmith's development history. For how to *use* SpoolSmith, see the
[documentation site](https://spilloid.github.io/spoolsmith/guide/). Read these files as
dated records: each describes the code and decisions at the time it was written, and
later releases may have changed both.

| File | What it is |
|---|---|
| [dev-process.md](dev-process.md) | Running log of every unit of work: who built it, how it was reviewed, defects found and fixed. Newest first. |
| [milestone-1-spec.md](milestone-1-spec.md), [milestone-1-fix-01.md](milestone-1-fix-01.md) | Fixture-only detection and catalog resolution, plus its fail-closed fixes. |
| [milestone-2-spec-probe.md](milestone-2-spec-probe.md), [milestone-2-fix-01-probe.md](milestone-2-fix-01-probe.md) | Live SNMP/HTTP/PJL evidence collection. |
| [milestone-2-spec-install.md](milestone-2-spec-install.md), [milestone-2-spec-install-addendum.md](milestone-2-spec-install-addendum.md), [milestone-2-fix-01-install.md](milestone-2-fix-01-install.md) | Windows install automation and its review round. |
| [milestone-3-spec-gui.md](milestone-3-spec-gui.md) | Native desktop app and action log. |
| [daily-use-spec.md](daily-use-spec.md) | Known-IP mapping, discovery and per-printer profiles (JSON at the time; `.ssb` since v1.1). |
| [ci-cd-spec.md](ci-cd-spec.md) | CI and release workflow. |
| [v0.7.3-polish-spec.md](v0.7.3-polish-spec.md) | UX pass and bulk driver export. |
| [v1.3-gui-spec.md](v1.3-gui-spec.md) | Desktop rebuilt around copy-off, double-click-on, and the apply sheet. |
| [v1.4-windows-driver-spec.md](v1.4-windows-driver-spec.md) | USB copies, WSD conversion and Windows automatic IPP setup. |
| [v1.5-windows-update-driver-spec.md](v1.5-windows-update-driver-spec.md) | Asking Windows for the printer's own driver after IPP setup. |
| [offline-intune-reflection.md](offline-intune-reflection.md) | Pre-pilot review of offline provisioning and Intune packaging. |
| [real-hardware-verification.md](real-hardware-verification.md) | Runbook for validating on real Windows and printers. |
| [capture-report-review.md](capture-report-review.md) | Review of an external research report on capture-and-redeploy. |

Dated test evidence lives in [`../validation/`](../validation/); remaining work is in
[`../roadmap.md`](../roadmap.md).
