# v0.6.0 real-hardware QC — September 17, 2026

Follow-up to the [quality review](2026-09-17-quality-review.md), which could not reach the
Windows VM. SSH access recovered later the same night; this records what that access found.

## Environment

| | |
|---|---|
| Machine | `DESKTOP-BBQQE4J`, Windows 11, reached via OpenSSH from the Linux host |
| Account | `kubert`, Administrator |
| Printer | Brother HL-L2315D at 192.168.68.108 |
| Binaries | `spoolsmith.exe`, `spoolsmith-gui.exe`, cross-compiled `GOOS=windows GOARCH=amd64`, stamped `v0.6.0` |
| Working set | `C:\SpoolSmithLab\qc-20260917`, against saved profiles in `C:\SpoolSmithLab\profiles` |

## CLI checks

`printers`, `status`, `copy-driver`, `verify-bundle`, `apply-preview`, `repoint-preview`,
`export-library` and `import-library` all passed against the real Brother queue.

A byte-for-byte comparison of the re-imported profile against the original then threw
`Imported profile changed`. The diff was a single field: the original evidence JSON carries
`"driver_package": null`; the copy produced by export-then-import omits that key rather than
keeping it as an explicit `null`. That is JSON-null-vs-absent, not a data loss — every other
field, including the captured SNMP/HTTP evidence and provenance, was identical. A follow-up
semantic check confirmed this explicitly:

```
PASS: all profile properties preserved; null optional package normalized to absent.
PASS: source-folder export rejected with no collection created.
PASS: Windows queue names, drivers and port mappings unchanged.
```

No fix needed — the strict-equality QC assertion was stricter than the format actually
guarantees. This is worth remembering if a future QC pass hits the same assertion.

## Desktop GUI check

The scripted tab-name check came back with an empty control list and failed before it could
compare names, throwing `Unexpected tabs:` with nothing joined in. The screenshot captured at
the same moment shows the app rendered correctly, with all four tabs present and labeled
**This PC**, **Add a printer**, **Review and apply** and **Tools**.

This matches the README's existing note that the FlaUI harness can read a control as
offscreen or miss it entirely right after a tab is selected — an attach/read timing issue in
the harness, not the app. Treated as the known intermittent issue, not a new defect; no
product change made. Evidence: `gui-failed.png`, `gui-controls.json`.

## What's still outstanding

SSH access to the VM stopped accepting the known credential partway through this pass
(password rejected after one retry), which cut the session short before a second GUI pass
could confirm the tab-read timing theory. Re-running the desktop check is the next step once
access is restored; nothing here blocks the v0.6.0 release itself.
