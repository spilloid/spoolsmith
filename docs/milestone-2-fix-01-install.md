# Fix + upgrade spec — internal/install review findings, plus the addendum

Found by independent Codex review (`-s read-only -c model_reasoning_effort=high`). Three High, three
Medium, all confirmed by the orchestrator — including verifying the most serious claim directly
against Microsoft's own documentation before accepting it (quoted below). Fix all six, then
implement the three addendum items from `docs/milestone-2-spec-install-addendum.md` in the same
round, per the operator's own request to fold them in here rather than as a separate cycle.

## Confirmed findings — fix all six

### 1. HIGH — `powerShellString` doesn't escape PowerShell's "smart quotes," enabling injection

Confirmed verbatim against Microsoft Learn's `about_Quoting_Rules`: *"PowerShell treats smart
quotation marks, also called typographic or curly quotes, as normal quotation marks for
strings... smart quotation marks also need to be escaped."* `powerShellString` in `plan.go` only
escapes backtick, `$`, and ASCII `"` — missing U+2018 (‘), U+2019 (’), U+201C ("), U+201D ("). A
value containing one of these can break out of the quoted string. This is reachable **today**:
`uninstall <printer-name>` takes raw CLI input straight into `LookupPrinter`'s PowerShell command,
before any plan is shown or confirmed — so this is an unconfirmed-mutation-bypass path, not a
theoretical one.

**Fix:** add all four smart-quote characters to the replacer in `powerShellString`, each escaped
with a preceding backtick (the same pattern the docs show: `` `" ``). Also reject any string
containing a NUL byte at the point a `Plan`/argument is constructed — Windows can't represent NUL
in a process command line, so a NUL-containing value should be a validation error, not something
that reaches `exec.Command` at all.

### 2. HIGH — cmdlet non-terminating errors can be silently swallowed

PowerShell's default `$ErrorActionPreference` is `Continue` — a cmdlet failure writes an error
record and execution *continues*; the `powershell.exe` process can still exit 0. None of the
generated commands (`Add-PrinterPort`, `Add-Printer`, `Remove-Printer`, `Remove-PrinterPort`,
`Remove-PrinterDriver`, `Get-PrinterDriver`, `Get-Printer`, `Get-PrinterPort`) force
`-ErrorAction Stop`, so `runPowerShell`'s exit-code check can report success when a cmdlet actually
failed — a direct violation of the fail-closed promise this whole unit exists to keep.

**Fix:** wrap every generated command so success/failure is unambiguous regardless of PowerShell
version quirks, rather than relying on undocumented exit-code behavior for `-Command`. Generate
each command as:
```
$ErrorActionPreference = 'Stop'; try { <cmdlet -ErrorAction Stop> } catch { Write-Error $_.Exception.Message; exit 1 }
```
This makes the process's own exit code unambiguous (0 = no exception, 1 = something threw) without
depending on version-specific implicit exit-code propagation. **State plainly in your report that
this wrapping pattern is the most defensible design achievable without a real Windows machine to
test PowerShell's actual exit-code behavior against — it is not itself claimed to be verified on
real hardware.**

### 3. MEDIUM — `DriverPresent` discards the real error, mislabeling infrastructure failures as "absent"

`DriverPresent` returns `(err == nil, nil)` — any error (access denied, module load failure,
transient failure) becomes `ErrDriverNotPresent`, even though the actual cause has nothing to do
with the driver being missing. This is fail-closed but diagnostically dishonest.

**Fix, kept simple deliberately rather than guessing at exact PowerShell exception types this
sandbox can't verify:** stop discarding the real error. `DriverPresent` returns `(false, err)`
with the actual captured error text when the check itself fails, distinct from a clean
"not present" result. `Preflight` and the CLI surface that real error text alongside the generic
"install the driver first" guidance, rather than always showing the generic message regardless of
what actually went wrong.

### 4. HIGH — partial-mutation results are discarded by the CLI on error

`Install`/`Uninstall` correctly append every attempted command (including a failed one) to
`Result.Ran` before returning an error. `main.go`'s `runInstall`/`runUninstall` call
`fatalCommand()` on any error **without ever printing that result** — so if `Add-PrinterPort`
succeeds and `Add-Printer` fails, the technician sees only a bare error, not that a port now exists
and needs cleanup, nor the actual PowerShell diagnostic output.

**Fix:** on any error from `Install`/`Uninstall`, print `result.Ran` (command, output, whether it
errored) before printing the fatal error message. The information already exists in the struct;
it just needs to reach the person who has to clean up.

### 5. MEDIUM — control characters in a value can make the displayed plan visually deceptive

Even once finding 1 is fixed (so the *executed* command can't be hijacked), a value containing a
newline, carriage return, or bidirectional-override control character can still make the *printed*
plan look like something other than what actually runs — relevant today for uninstall's
free-text `printer-name` argument, and more so once the addendum's forced-override path exists.

**Fix:** reject (don't sanitize-and-silently-continue) any value used in a `Plan` field that
contains a C0 control character (below U+0020, excluding none — no exceptions) or a bidi-control
code point (U+202A–U+202E, U+2066–U+2069) at construction time, with a clear validation error.

### 6. MEDIUM — the Brother (and, unverified, the HP) driver name is a package label, not a confirmed Windows driver identity

`Add-Printer -DriverName`/`Get-PrinterDriver -Name` need the *exact* name Windows registers a
driver under. `catalog.DriverPackage.Name` currently holds a human-readable package description
("Brother model-specific Full Driver & Software Package," "HP Universal Print Driver for Windows
PCL 6") and that same string is used as the literal `-DriverName` argument. Neither name has been
confirmed against real `Get-PrinterDriver` output — this is a real, disclosed unknown, not
something to guess convincingly and present as fact.

**Fix:** add `catalog.DriverPackage.WindowsDriverName string`, distinct from `Name` (which stays
the human-readable label for display/citation). Leave `WindowsDriverName` **empty** for both
families for now — do not invent a plausible-looking value. `install.BuildPlan` must fail closed
with a clear, specific error ("Windows driver name not yet verified for this family — run
`Get-PrinterDriver` after staging the real vendor package on a Windows machine and populate
`WindowsDriverName`") whenever `WindowsDriverName` is empty. This means install will not actually
run for either family until the operator does this real-hardware lookup once per family and fills
in two strings — state this plainly as the actual remaining blocker, not an engineering gap.

## Then implement the addendum (`docs/milestone-2-spec-install-addendum.md`), with one correction

The addendum's exit-code contract contradicts itself between its own paragraphs on dry-run (one
place implies code 0 for a passed dry-run, another assigns code 5). **Resolved here:** dry-run
that passes preflight is exit `0` — a genuinely successful health check. Exit `5` means a *real*
(non-dry-run) attempt where the human explicitly declined confirmation; a dry-run never reaches
the confirmation step at all, so it can never legitimately produce code 5.

Per the review's own "implementation friction" note, do the refactor it recommends as part of this
same round, not after: move detection/resolution branching, preflight selection, and confirmation
logic out of `main.go`'s command handlers and into `internal/install`-callable functions that take
an `io.Writer`/reader rather than calling `os.Exit`/touching global `os.Stdin` directly — this is
what actually makes the addendum's "CLI is a thin wrapper" rule true, and what makes the
stdout/stderr-separation and exit-code contract testable without spawning the real binary.

Implement all three items from the addendum spec: `catalog families`, `--force-family` (with
`Plan.ForcedOverride`), the interactive single-choice picker with an injectable reader for tests,
`--dry-run`/`--what-if` via the `Preflight`-extraction pattern already partially present, and the
full stdout-is-JSON/stderr-is-interactive/exit-code-contract rule across every command.

## Verification required before reporting done

- `go build ./...`, `go vet ./...`, `GOOS=windows GOARCH=amd64 go build ./...` (reproduce
  directly), `go test ./... -v -count=1` — all clean, pasted output.
- New tests for each of the 6 numbered findings above, each asserting the fixed behavior
  specifically (the exact smart-quote injection string from the review, a simulated
  non-terminating-error case, a partial-failure case asserting `result.Ran` reaches output, a
  control-character rejection case, `WindowsDriverName`-empty fail-closed case).
- New tests for the addendum per its own verification section (exit codes per branch, stdout is
  valid JSON / stderr carries interactive text, `--force-family` valid/invalid, picker abort/select,
  `--dry-run` never calls a mutating `env.Run` in any preflight outcome).
- State plainly which parts remain unverifiable from this sandbox (real PowerShell exit-code
  behavior for finding 2's wrapper pattern; the real Windows driver names finding 6 now blocks on).
