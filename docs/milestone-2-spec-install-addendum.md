# Milestone 2, Unit 2 addendum — pipeline-safe CLI, forced family override, dry-run

Written by the orchestrator, folded into the same review round as `docs/milestone-2-spec-install.md`
rather than a separate dispatch cycle, per the operator's own request: treat these as recommended
upgrade items the independent review surfaces alongside its normal security/correctness pass, then
implement them in the same fix round as whatever else the review finds. Read the base install spec
first — this addendum extends it, doesn't replace it. None of this changes the non-negotiable line:
one explicit confirmation before any mutation, still true even when a family is manually forced.

## 1. CLI/GUI parity and pipeline-safe infra

**Rule, stated explicitly rather than left implicit:** the CLI contains zero business logic. Every
command is a thin wrapper calling exported functions in `internal/probe`, `internal/catalog`,
`internal/install` — this is already true by construction, but make it load-bearing:

- **stdout is always machine-parseable JSON**, for every command, every time — `inspect`,
  `catalog probe`, `catalog families` (new, see §2), `install`, `uninstall`. This is what makes the
  CLI and any future GUI provably equivalent: a GUI just calls the same Go functions directly, and
  a script gets the same shape a GUI would render, on stdout, unconditionally.
- **Anything interactive (the human-readable plan summary, the confirmation prompt) goes to
  stderr**, never stdout. A script piping stdout through `jq` never sees prompt text mixed in.
- **Exit codes are a stable contract**, not incidental:
  - `0` — success (including a dry-run whose preflight checks all passed).
  - `1` — general/unexpected error (I/O, network, etc.).
  - `2` — usage error (bad arguments) — unchanged from today.
  - `3` — detection ran but did not resolve to an actionable family/driver, and no forced
    override was given.
  - `4` — preflight failed (not elevated, or the named driver is not present in the Windows
    driver store) — returned whether or not `--dry-run` was passed, since dry-run's whole point is
    surfacing this without mutating anything.
  - `5` — not confirmed (the human declined, or `--dry-run` was passed — dry-run is "confirmed:
    false, by design," not an error, but scripts need to tell "would have worked" apart from
    "actually ran," so this code means specifically "did not execute, no error otherwise").
- **Never block on stdin** when `--yes`, `--json`, or `--non-interactive` is passed (add
  `--non-interactive` as the explicit flag name; `--yes` implies it). A script that forgets to pass
  a flag and hits a case that would otherwise prompt should fail loudly (exit 3/4/5 as appropriate)
  rather than hang forever waiting on a TTY that doesn't exist.

## 2. Forced family override when detection doesn't resolve (or is overridden deliberately)

`catalog.Resolve` staying fail-closed is correct and unchanged — this adds an explicit, human-
initiated override path *around* an unresolved (or even resolved-but-disagreed-with) automatic
result, never a change to the automatic algorithm's own honesty.

- **New subcommand: `spoolsmith catalog families`** — prints every entry from `catalog.Families()`
  (ID, manufacturer, aliases) as JSON. This is what makes the override discoverable/scriptable
  rather than requiring someone to read source code to find a valid family ID.
- **New flag on `install`: `--force-family <id>`** — the pipeline-safe path. Skips
  `catalog.Resolve` entirely for family selection; builds the plan directly from
  `catalog.Families()`/`catalog.DriverFor(id)` for the given ID. Errors immediately (exit 2) if the
  ID doesn't exist in the catalog.
- **Interactive fallback, no flag, unresolved detection, real TTY:** if `catalog.Resolve` returns
  unresolved and `--force-family`/`--non-interactive`/`--yes`/`--json` were not passed and stdin is
  a terminal, print the `Uncertain` reasons plus a numbered list from `catalog.Families()` to
  stderr and prompt: `Select a family by number, or 'a' to abort:`. This is a single-choice picker
  (choosing which one family to force), not a true multi-select — "multiselect" read as "choose one
  from several," which is what actually makes sense for "which family is this printer." Anything
  other than a valid number aborts (exit 5), same discipline as the confirmation prompt.
- **Non-interactive, unresolved, no override given:** exit 3 immediately. Never hangs.
- **Provenance stays honest either way:** `Plan` gets a new field, `ForcedOverride bool` (true for
  both the flag and the interactive-picker paths). The printed plan — JSON and the stderr
  human-readable summary — states plainly when a family was manually forced rather than
  automatically resolved with confidence, e.g. `"resolution": "forced-override"` vs.
  `"resolution": "automatic"` in the JSON, and a `⚠ Family manually selected — not an automatic
  high-confidence match` line in the stderr summary. A forced override does not get to borrow the
  automatic path's confidence framing.
- Forcing a family does **not** skip the confirmation step or the preflight checks (elevation,
  driver presence) — it only changes how the family/driver in the plan got chosen. Everything
  downstream of `BuildPlan` is identical either way.

## 3. Dry-run / what-if (naming matches PowerShell's own `-WhatIf` convention, deliberately)

`--dry-run` (alias `--what-if`) on both `install` and `uninstall`. This is a real hygiene/health
check — "would this actually work right now" — not a no-op:

- Runs detection/resolution (or the forced-override path), `BuildPlan`, and the full preflight
  check (elevation, driver presence) exactly as a real attempt would.
- **Never calls `env.Run` for anything that mutates, and never prompts for confirmation** — this is
  the one absolute rule, whether or not `--yes` is also passed (if both are given, `--dry-run`
  wins; document that precedence explicitly rather than leaving it ambiguous).
- **Exit code reflects what a real attempt would have hit at that point** — 0 if preflight passed
  (a genuinely useful "yes, this would work" signal for a script or a pre-flight sweep across many
  printers), 3/4 if detection or preflight would have failed, same as a real run. This is what makes
  it a hygiene check rather than a formality: a technician (or a script) can dry-run a whole
  building's printers and get a real, meaningful yes/no per device before touching anything.
- Refactor `install.Install`'s internals so the preflight logic is its own function
  (`Preflight(ctx, env, plan) (PreflightResult, error)`) that both `Install` and the CLI's dry-run
  path call directly — `Install` should not itself need a dry-run parameter; the CLI decides
  whether to call `Preflight` alone (dry-run) or `Install` (which calls `Preflight` internally, then
  executes only if confirmed).

## Verification required before reporting done

- Everything from the base spec's verification section, still required.
- New tests: `catalog families` output is valid JSON containing exactly `catalog.Families()`;
  `--force-family` with a valid/invalid ID; the interactive picker's abort path and valid-selection
  path (inject a fake stdin reader — do not require a real TTY to test this); `--dry-run` on both
  `install` and `uninstall` never calls a mutating `env.Run`, in every preflight outcome (elevated+
  present, not elevated, driver absent); exit codes match the contract above for every branch
  (unresolved, preflight-failed, not-confirmed, dry-run, success) — table-driven tests are
  appropriate here given how many branches this is.
- Confirm stdout is valid JSON and stderr carries only the human-readable/interactive text, for at
  least one case per command (`inspect`, `catalog probe`, `catalog families`, `install`,
  `uninstall`), by asserting on captured stdout/stderr separately in tests, not just that the
  command didn't error.
