# Milestone 2, Unit 2 — Windows install automation (`internal/install`)

Written by the orchestrator before dispatch, per STD-001. This is the highest-stakes unit in this
repo — governed by `corporate-strategy/decisions/DECISION_LOG.md` D-0040, which authorizes exactly
this and nothing more: driver install + TCP/IP port configuration for two named families (HP
LaserJet Pro M4xx; Brother HL-L2xxx), via the trust model below, with one required confirmation
before any mutation, no exceptions. Read D-0040 in full before touching this file if you weren't
given it directly.

## Design decision made here, stricter than the original plan sketch — read this first

The original plan sketch considered SpoolSmith directly invoking a vendor installer EXE with a
"documented silent switch." **This spec does not do that.** Nobody on this task has verified HP's
or Brother's actual current silent-install CLI syntax against a real installer in hand — asserting
a specific flag as fact when it hasn't been checked against the real thing would be exactly the
kind of unverified claim this repo's own discipline exists to catch. Instead:

**SpoolSmith only ever calls Windows' own printer-management PowerShell cmdlets
(`Add-PrinterPort`, `Add-PrinterDriver`, `Add-Printer`, `Get-PrinterDriver`,
`Remove-Printer`/`Remove-PrinterPort`) against a driver that must already be present in the
Windows driver store.** It never shells out to a vendor EXE/MSI with a guessed flag. If the named
driver isn't present, the install fails closed with an actionable message ("driver not found —
install it via Windows Update or run the vendor package manually first, then retry") rather than
attempting to fetch or silently invoke anything. This is safer, more honest given real uncertainty
about vendor CLI syntax, and still satisfies D-0040's trust model (Windows' own Authenticode
signature check governs whatever driver actually lands in the store, regardless of how it got
there).

## Package: `internal/install`

Split by build constraint so the core stays testable without a Windows machine — **this sandbox
has none**, so anything not structured this way cannot be verified here at all:

### `plan.go` — no build tag, pure, fully testable on any platform

```go
package install

// Environment is the seam between orchestration logic and the real OS. The
// real implementation lives in windows.go (Windows-only); tests use a fake.
type Environment interface {
    IsElevated(ctx context.Context) (bool, error)
    DriverPresent(ctx context.Context, driverName string) (bool, error)
    Run(ctx context.Context, command string) (output string, err error)
}

type Plan struct {
    IPAddress   string
    PrinterName string
    PortName    string
    DriverName  string
    Family      catalog.Family
    Driver      catalog.DriverPackage
    Commands    []string // exact PowerShell command lines, in order — this IS the reviewable plan
}

// BuildPlan fails if resolution is not fully resolved (nil Family or Driver) —
// there is nothing to build a plan for. Naming: PortName = "SpoolSmith-<ip>",
// PrinterName = the resolved NormalizedModel string. Commands must be built
// here, as literal strings, so they can be shown to the user verbatim and
// tested without touching Windows.
func BuildPlan(ip string, resolution catalog.ResolutionResult) (Plan, error)

type Result struct {
    Plan Plan
    Ran  []CommandResult
}
type CommandResult struct {
    Command string
    Output  string
    Err     error
}

var ErrNotElevated = errors.New("...")
var ErrDriverNotPresent = errors.New("...")
var ErrNotConfirmed = errors.New("...")

// Install runs the fail-closed preflight (elevation, driver presence) against
// env, and only executes plan.Commands if confirmed is true. This function
// contains ALL the orchestration logic and must be fully testable against a
// fake Environment — this is where "does it actually fail closed" gets proven,
// not in windows.go.
func Install(ctx context.Context, env Environment, plan Plan, confirmed bool) (Result, error)

// Uninstall removes exactly what a prior install recorded: the printer and
// its port. It does not remove the driver itself (a rarer, more disruptive
// action) unless purgeDriver is true.
func Uninstall(ctx context.Context, env Environment, printerName, portName, driverName string, purgeDriver bool) (Result, error)
```

### `windows.go` — `//go:build windows`, thin and mechanical on purpose

Real `Environment` implementation. Keep this file as small and boring as possible — it should only
translate `Environment` method calls into `exec.CommandContext(ctx, "powershell.exe", "-NoProfile",
"-Command", ...)` calls and return output/errors. All the "what commands, what order, what fails
closed" logic belongs in `plan.go`, not here, because this file cannot be executed or its logic
verified in this sandbox at all — minimizing what lives here minimizes what ships unverified.

- `IsElevated`: run `([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)`, parse `True`/`False` from trimmed output.
- `DriverPresent`: run `Get-PrinterDriver -Name "<name>"` (quote/escape the name properly), treat a non-error exit as present, any error as not present.
- `Run`: execute the given command string via `powershell.exe -NoProfile -Command`, return combined output and any error. Commands themselves come from `Plan.Commands`, built in `plan.go` as: one `Add-PrinterPort -Name "<port>" -PrinterHostAddress "<ip>"`, one `Add-Printer -Name "<printer>" -DriverName "<driver>" -PortName "<port>"`.

### `other.go` — `//go:build !windows`

An `Environment` implementation (or a constructor that returns one) whose every method returns a
clear `errors.New("install: windows-only, not supported on this platform")`. This is what makes
`go build ./...`/CI on Linux and this dev sandbox keep working.

## CLI (`cmd/spoolsmith/main.go`)

```
spoolsmith install <ip> [--yes]
```
1. `probe.Collect` (from Unit 1) then `catalog.Resolve` — silent, automatic, no per-field prompts.
2. If unresolved: print `Uncertain`, exit non-zero. Nothing to confirm — do not print a plan for
   an unresolved result under any circumstance.
3. If resolved: `install.BuildPlan`, then real preflight via the real `windows.go` `Environment`
   (elevation + driver presence) — fail closed with the actionable message on either failure,
   before ever printing a plan that can't actually run.
4. Print the plan (human-readable summary + the exact commands). Unless `--yes` was passed,
   prompt on stdin for exactly one explicit confirmation (`y`/yes, anything else aborts). This
   step is not skippable by any flag other than `--yes`, and `--yes` still prints the full plan
   first — it only skips the interactive keypress for scripted use, per the plan's own note.
5. Call `install.Install` with `confirmed` set from step 4. Print the `Result`.

```
spoolsmith uninstall <printer-name> [--purge-driver] [--yes]
```
Same confirmation discipline. Looks up the printer's port/driver via `Get-Printer`/`Get-PrinterPort`
(a small addition to `windows.go`'s `Environment` if needed, or infer from naming convention if
this proves simpler — implementer's call, but state which approach was taken).

## Explicitly out of scope for this unit

- Invoking any vendor installer EXE/MSI directly (see design decision above).
- Any network fetch of a driver package.
- SpoolSmith self-elevating (e.g. a UAC re-launch) — it checks and fails closed instead.
- macOS/Linux install support.
- Any change to `internal/catalog`, `internal/evidence`, or `internal/probe` (Unit 1) beyond
  calling their existing public functions.

## Verification required before reporting done

- `go build ./...` and `go vet ./...` clean on this (Linux) sandbox — confirms `other.go`'s stub
  compiles and the package as a whole builds without Windows.
- `GOOS=windows GOARCH=amd64 go build ./...` — confirms `windows.go` at least compiles for the
  real target. **Reproduce this directly yourself; do not just assert it works** — a prior unit in
  this same repo had a dispatcher falsely report a Windows cross-build failure that turned out not
  to be real, so don't let this be trusted on a single report either way.
- `go test ./... -v -count=1` clean, including new tests for `BuildPlan` (correct command strings
  for both families, fails on unresolved input) and `Install` (fails closed on not-elevated,
  fails closed on driver-not-present, does not call `env.Run` at all unless `confirmed` is true,
  and — when confirmed — runs exactly the commands from the plan in order) — all against a fake
  `Environment`, all running on Linux.
- State plainly, in your final report: `windows.go`'s actual behavior against a real Windows
  machine has **not** been verified by this dispatch and cannot be from this sandbox. That
  verification is the operator's, on real hardware, not something to imply happened.
