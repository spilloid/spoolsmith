# Native GUI + action-log observability (D-0042)

Operator direction, 2026-09-06, continuing past the `v0.2.0` release: a native desktop
GUI reaching CLI feature parity, plus per-action logging to a local temp file. Recorded
as direct operator direction, not a new board motion (`corporate-strategy` D-0042) —
this is a new front-end over already-authorized capabilities (D-0039/D-0040/D-0041),
not an OS-mutation scope expansion.

## Architecture

- `cmd/spoolsmith-gui`, a second binary, Windows-only (`github.com/tailscale/walk`,
  a maintained fork of the unmaintained `lxn/walk` pure-Go Win32 wrapper, zero cgo).
  The upstream `lxn/walk` (last commit 2021) crashes on this real, current Windows
  build the moment any widget with tooltip support is created (`TTM_ADDTOOL failed`,
  a `TOOLINFO`-struct/comctl32-version mismatch) — confirmed directly by launching
  the built exe, not assumed. Tailscale maintains a fork for their own Windows GUI
  that fixes exactly this; switching to it (mechanical import-path swap, same
  package shapes) requires raising this module's `go` directive to 1.24 and both
  CI legs' pinned Go version to `1.24.x` to match the fork's own `go.mod`. A
  `//go:build !windows` stub package keeps `go build ./...`/`go vet ./...` green on
  the `ubuntu-latest` CI leg, mirroring `internal/install`'s existing
  `windows.go`/`other.go` split. A side-car `spoolsmith-gui.exe.manifest` (comctl32
  v6 + per-monitor DPI awareness) ships next to the exe, matching the fork's own
  examples.
- The GUI imports `internal/{probe,catalog,inspect,install}` directly and calls the
  same `install.Workflow.RunInstall`/`RunUninstall` the CLI calls — no shelling out to
  `spoolsmith.exe`, no second mutation path. `Workflow`'s function fields were already
  built for this (`workflow.go`: "Function fields are public to let a future GUI ...
  supply the same seams").
- Confirmation gate: **Preview** always forces `DryRun: true` — the identical call
  the CLI's own `--dry-run` makes, so the plan/preflight text shown is byte-for-byte
  what the CLI would print, and nothing can mutate during a preview (dry-run
  short-circuits before `RunInstall`/`RunUninstall` ever reach their confirmation
  branch). **Execute** only becomes clickable after a successful Preview, and itself
  requires a native Yes/No `walk.MsgBox` naming the operation before doing anything;
  only then does it re-run the identical call with `Yes: true, NonInteractive: true`
  (the CLI's own `--yes --non-interactive` contract) — no interactive stdin/pipe
  plumbing needed, and every branch this exercises (`DryRun`, `Yes`+`NonInteractive`)
  is already covered by `internal/install`'s existing tests. Preview-then-click-
  Execute-then-confirm-the-dialog is the GUI's one required explicit confirmation of
  a fully shown plan, matching D-0040's gate.
- Feature parity checklist against `main.go`/`daily.go`: `discover`, `inspect`,
  `catalog families`/`catalog probe`, `profile capture`/`profile edit`,
  `install`/`add`/`configure` (including `--force-family`, `--profile`), and
  `uninstall`/`remove` (including `--purge-driver`, `--profile`). JSON/pipeline mode
  has no GUI analogue (the GUI itself is the interactive surface).

## Action-log observability

`internal/actionlog`: one JSON-line-per-action file at
`os.TempDir()/spoolsmith/actions.log`, written by both the CLI and the GUI so a
session's command/click sequence is reconstructable after the fact. Fields: UTC
timestamp, `source` (`cli`/`gui`), `operation`, `args` (already non-secret — this app
collects no credentials, per its own `CLAUDE.md`), `status`, `exit_code` (CLI) or
`error`, and duration. Local-only, never transmitted; rotates to `actions.log.1` past
5 MB so it can't grow unbounded. A logging failure (e.g. an unwritable temp dir) must
never block or fail the underlying command — it degrades to a one-time stderr warning
and a no-op logger, matching this repo's "no probe should be load-bearing" rule.

The CLI wires this at exactly one point (`main()`, wrapping `run()`'s result) rather
than scattering log calls through already-reviewed business logic, so existing
CLI tests are unaffected. The GUI logs at each user-triggered action, the same
granularity as one CLI invocation.

## Non-goals

No new install families, no network auto-fetch, no change to the D-0040 trust model,
no telemetry/network transmission of the action log, no GUI path that skips the
confirmation gate under any control.
