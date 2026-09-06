# Daily-use workflow contract

Operator direction, 2026-09-06: prioritize using SpoolSmith for daily printer
mapping, with known-IP and discovery paths and one reusable JSON per printer.
Astra implements; Claude Opus was requested as a read-only contract reviewer.
This direction expands the old two-family mapping scope to explicit operator
profiles using an already-installed Windows driver. Driver downloads and package
execution are not part of this change.

- `discover <IPv4 CIDR>` scans at most 256 addresses with bounded concurrency,
  no ICMP prerequisite, and returns candidates with evidence and resolution.
  Open printer ports identify candidates, never driver compatibility.
- `profile capture <target> <file> --name <queue> --driver <registered name>`
  captures live evidence and writes a versioned, declarative profile exclusively
  (never overwrites). Profiles contain no shell commands or installer URLs.
- `install --profile <file>` re-probes the saved target. Available saved model
  sources must agree; at least one HTTP/PJL source must match when the capture has
  either. SNMP-only captures can match SNMP, but SNMP cannot replace saved model
  sources. Unavailable identity is retried once; conflicts fail immediately. This is
  observed model continuity, not cryptographic identity or driver certification.
  Profile driver choice is explicitly operator-selected, never automatic.
- Profiles support printers outside the built-in catalog without pretending a
  catalog resolution occurred. Exact driver presence, elevation, plan display,
  confirmation, dry-run precedence, and partial-result reporting are preserved.
- Local profiles belong in ignored `profiles/`; no client inventory is committed.
- Tests cover discovery bounds/cancellation/candidate classification and profile
  parsing, identity mismatch, command injection, dry-run, and confirmation.

Completion evidence must distinguish automated tests from real printer mapping.
Actual Windows installation requires a suitable locally registered driver, or the
optional reviewed Brother local-archive recipe described below.

## Local package staging follow-up

- Optional `driver_package: {id, archive}` references an embedded reviewed source
  record. Only the verified Brother HL-L2315D Windows x64 recipe is executable today.
- `profile edit --package <id> --archive <path>` attaches it; `--clear-package`
  removes it. Relative archive paths resolve against the profile directory at use.
- One reviewed confirmation covers package staging and queue mapping. Dry-run has
  no archive reads/extraction or Windows mutations. Package signatures are checked
  during execution, so a successful preview is not a verified payload claim.
- Reuse a registered driver without opening the archive. For a missing driver,
  hold the archive read-only, verify its pinned hash and vendor signature, list
  safe paths, extract with Windows tar, verify the catalog signature, stage the INF,
  register the exact driver, and verify registration before queue commands.
- Stop on any failed package step. Partial staging is reported; there is no automatic
  rollback. Temporary extraction directories are retained. Downloads remain separate.

## Operator clarification: idempotency and UX

The operator explicitly emphasized idempotency, strong UX, and easy add/remove/config.

- `add` aliases install; matching queues/ports are reused. A conflicting port is
  never overwritten; a conflicting queue requires `configure`.
- `configure --profile` enables an explicit, reviewed queue driver/port update.
  Changing the profile queue name creates a new queue; it does not rename/delete
  the old queue. Old ports are retained when moving a queue.
- `remove --profile` aliases uninstall by the saved queue name. An absent queue
  succeeds without changes; shared ports/drivers and externally named ports are
  retained. Removal rechecks queue configuration against the shown plan.
  Profile removal also requires the installed port/driver to agree with the profile.
- `profile edit` updates selected settings while preserving a backup. Capture
  creates missing parent directories and never overwrites a profile.
  Backups are `.bak` files under `.backups/`, outside printer JSON globs.
- Terminal install/configure/remove commands show human output; redirection and
  `--json` retain machine-readable results. Help succeeds without a usage error.
  Human plans show the queue, target, RAW protocol/port, exact driver and mutation
  policy; `--dry-run --json` exposes full literal commands. This UX refinement follows
  the operator's explicit request for strong UX rather than exposing long guard scripts
  in every routine terminal confirmation.
- Discovery handles Ctrl-C locally; install confirmation retains normal terminal
  interrupt behavior. Do not globally intercept Ctrl-C with a non-cancellable reader.
- Verify generated command semantics in real PowerShell using in-memory cmdlet
  doubles: repeat add/remove, configuration update, conflicts, and shared-port retention.
