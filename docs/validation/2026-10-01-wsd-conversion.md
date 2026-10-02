# WSD to IP conversion validation — 2026-10-01

The source PC had two separate Brother HL-L2315D queues. The existing RAW 9100 queue
(`SpoolSmith VM Brother`) was left unchanged. A second queue, `Brother HL-L2315D series
Printer`, used port `WSD-d4c36f93-aeca-4ed7-a165-bdb795c4e231` and the Microsoft IPP
Class Driver.

Read-only source checks established the following chain:

1. The WSD port monitor reported device ID
   `urn:uuid:e3248000-80ce-11db-8000-30c9ab962e73` through XcvData.
2. Windows' present WSD PnP device with that exact ID reported
   `http://192.168.68.108:80/WebServices/Device`.
3. A live WS-Transfer Get to that endpoint returned matching WSD device metadata.
4. An IPP Get-Printer-Attributes request returned the same printer UUID, model
   `Brother HL-L2315D series`, and canonical endpoint
   `ipp://192.168.68.108/ipp/print`.

`TestHardwareWSDConversion` passed using the installed WSD queue. The gated
`TestHardwareWSDCopyAndImportPreview` then wrote a temporary settings-only `.ssb`,
reopened and validated it, and produced a successful import dry run. Its plan uses
`Add-Printer -IppURL` with the verified literal-IP endpoint and Microsoft IPP Class
Driver; it has no WSD or RAW port creation command. `Add-Printer -WhatIf` accepted the
IPP command syntax without changing Windows.

This session's Windows token could query printers but could not add a temporary IPP
queue: `Add-Printer` returned *Access was denied to the specified resource*. An
actual IPP queue creation, cross-PC apply, and physical print job remain untested.
The implementation checks the created queue's driver and IPP endpoint after Windows
adds it and fails if it cannot verify either.

The test did not change or remove either installed Brother queue and sent no print
job. Microsoft documents the [WSD port monitor](https://learn.microsoft.com/en-us/windows-hardware/drivers/print/wsdmon-port-monitor)
and [`Add-Printer -IppURL`](https://learn.microsoft.com/en-us/powershell/module/printmanagement/add-printer).

## RAW preference and pending elevated validation

The final default now prefers an unambiguous existing RAW 9100 driver mapping at
the verified IP when the source WSD queue uses Microsoft IPP Class Driver. The
Brother's existing `SpoolSmith VM Brother` queue supplies `Brother HL-L2315D series`.
Both gated read-only hardware tests passed again with this default: the copied
file uses that OEM driver and imports through `RAW9100-192.168.68.108`.
The IPP path above remains the fallback when a RAW driver mapping is unavailable.

Before release, run `scripts/validate-printer-release.ps1` in an elevated session.
It exports the OEM payload, applies and reapplies a uniquely named temporary queue,
checks local status, validates USB offline preparation without creating a USB queue,
and separately exercises IPP fallback creation and reapplication. Temporary queues
are removed and installed drivers are retained. It sends no print jobs. The USB
check reuses an installed driver; first-time staging still needs a clean target.

## Elevated validation results

The elevated release validation ran on 2026-10-01. Its transcript is
`dist/validation/printers-20261001-202912.txt` (local build artifact).

- WSD identity resolution and the settings-only copy/import preview passed.
- OEM driver export, temporary RAW queue creation, reapplication, and compliant
  local status passed with `Brother HL-L2315D series` and
  `RAW9100-192.168.68.108`.
- USB offline preparation passed, reused the installed OEM driver, and created
  no USB queue. First-time staging on a clean target remains untested.
- IPP fallback failed on its first apply: `Add-Printer -IppURL` returned
  `The specified printer already exists` despite the unique requested queue name.
  A subsequent printer/port inventory showed no temporary validation queues and
  no IPP port. The existing Brother WSD and RAW queues remained present with
  their original drivers and ports. An interaction with the existing device
  registration is a possible cause, not yet established; IPP creation and
  reapplication remain unverified and block a fully passing hardware check.
- The separately gated metadata test skipped because its URL/device-ID
  environment variables were not set. WSD conversion itself verified live
  device identity successfully.

`go test ./...` and `go vet ./...` passed on Windows in the same session.
No print jobs were sent. Cross-PC apply and physical printing remain untested.

## Prepared-machine IPP retry

The operator authorized temporary source-queue removal to avoid the existing
device collision. `scripts/validate-printer-release.ps1 -PrepareIPPSource`
captures source settings before removing the WSD queue and restores it after the
temporary IPP queue is cleaned up. `-IPPOnly` limits retries to that check.
Backups are retained under `dist/validation`; shared queues and queues with
pending print jobs are refused. Restoration must verify the live WSD port,
because Windows can delete that port during queue removal and allocate a new
port ID during directed device discovery.

Preparation cleared the duplicate-printer error. The focused retry transcript,
`dist/validation/printers-20261001-205625.txt`, shows that Windows created the
requested queue with Microsoft IPP Class Driver but assigned `WSD Port Monitor`
and no exposed host address. SpoolSmith's IPP port/endpoint verification rejected
it and removed the new queue. This is now a reproducible verification limitation,
not merely a duplicate source queue. No successful IPP apply/reapply is claimed.

The initial restoration reused a deleted port; directed WSD discovery repaired
it, and the harness now recreates a missing port through the captured device URL.
The final focused run successfully restored the original driver and settings on
a new WSD port. Follow-up live WSD identity conversion and copy/import preview
tests passed. Final inventory contained the original four queues, including both
Brother queues, and no temporary validation queue. The RAW queue remained on
`RAW9100-192.168.68.108`; the restored WSD queue uses
`WSD-f02ca68b-bbaa-4ad6-ae22-8732bb523fc9` with Microsoft IPP Class Driver.
Focused IPP unit/PowerShell tests and install/bundle vet checks also passed.

## Windows-managed IPP verification completed

The prepared-source run in `dist/validation/printers-20261001-212053.txt`
passed real queue creation, reapplication, compliant local status, temporary
queue cleanup, and restoration of the source WSD printer. The IPP connection
uses Windows' WSD Port Monitor, so verification now requires its IPP protocol
metadata and the configured URL returned by the Windows bidirectional printer
API (`\\Printer.DeviceInfo.NetworkingInfo:IppDeviceUrl`). A monitor name or
reachable endpoint alone is insufficient. Earlier failed checks above remain
part of the validation history.

This establishes same-machine operation with an already installed inbox class
driver. Clean-target provisioning, cross-PC operation, and physical printing
remain separate unverified cases. Corporate direction is recorded as D0050 in
the adjacent corporate-strategy decision log; no board vote is asserted.

## Candidate checks after Windows automatic setup

- Full Windows `go test ./...` and `go vet ./...` passed.
- Windows CLI and GUI candidate builds passed (`v1.4.0-rc`, build only).
- The built CLI captured this Brother's IPP model and endpoint into
  `dist/validation/windows-automatic.ssb`; elevated direct-IP dry-run returned
  an IPP plan with Microsoft IPP Class Driver and made no queue changes.
- Final Windows inventory contained the four original queues. The restored
  Brother source has a live WSD monitor port; its port identifier changed during
  removal/restoration, as expected.
- GUI regression run: 32/34 passed. Startup and offline-review handoff checks
  failed in that run; isolated reruns of both plus the direct-IP screen passed
  (3/3). This is a timing/reliability concern, not a claim of an entirely green
  full GUI run. The direct-IP screenshot was inspected.
- No release was published or independent review claimed. Clean-target driver
  provisioning, cross-PC use, and physical printing remain unverified.
## Final v1.4.0 release checks

Final v1.4.0 CLI/GUI builds passed. Two further full GUI runs passed 33/34,
exposing a transient preview wait and an immediate Details text-control lookup.
Named-control lookups now use a bounded five-second rendering wait, matching
the existing button helper. The subsequent complete suite passed 34/34 against
the final binaries (3m38s), with unchanged content assertions. Earlier failures
remain recorded above; no physical printing or clean-target coverage is claimed.
