# CI/CD implementation spec (DR-0005 §8 — sequenced after the core unit, which now passes)

Mechanical CI/build work, not architecture — routes at a lower tier than the detection/catalog
core per `docs/dev-process.md`'s routing table and DR-0005 §2. Written by the orchestrator before
dispatch.

## Goal

GitHub Actions that builds this Go CLI and publishes a Windows release artifact (zip) on a
published GitHub Release, plus a checked-in PowerShell script that performs the exact same
build/package steps by hand — for anyone who wants to run them without trusting the pipeline.
Structured multiplatform-by-construction (a GOOS/GOARCH-driven matrix so adding macOS/Linux later
is a matrix-row addition, not a rewrite) but only Windows actually builds/publishes today, per
DR-0005 §8 — do not add macOS/Linux build/publish steps, only the *structure* that would make
adding them cheap later.

Mirror the shape of `netviz/.github/workflows/ci.yml` and `release.yml` (same company, same
pattern, cited as precedent in DR-0005 §8) — **but use GitHub-hosted runners
(`windows-latest`/`ubuntu-latest`), not `self-hosted`.** netviz's self-hosted runners are
registered to a different GitHub org/account than this repo (`spilloid/spoolsmith`); nothing here
should assume a self-hosted runner pool exists for this repo, and GitHub-hosted runners are
sufficient for a plain Go CLI with zero CGO/Node/desktop-framework dependencies.

## Files to create

1. **`.github/workflows/ci.yml`** — on `push` and `pull_request`: run `go build ./...`,
   `go vet ./...`, `go test ./... -v` on `ubuntu-latest` (fast feedback) AND `windows-latest` (the
   platform this ships on) as a matrix — this is also what makes "structured multiplatform"
   concrete rather than aspirational: CI already proves the code builds and tests green on more
   than one OS, even though only Windows gets a published release artifact.

2. **`.github/workflows/release.yml`** — triggers on `release: published` and
   `workflow_dispatch` (with a `tag` input, for manual testing without needing a real release —
   mirror netviz's exact pattern for resolving the tag from either source). One job,
   `runs-on: windows-latest`, matrix of one entry today (`{goos: windows, goarch: amd64}`) written
   so a second matrix row is the only change needed to add another platform later. Steps:
   - checkout, `actions/setup-go@v5` pinned to a Go 1.22.x-compatible version string
   - `go build -o dist/spoolsmith.exe ./cmd/spoolsmith`
   - stage `dist/spoolsmith.exe`, `README.md`, `LICENSE` into a `spoolsmith/` directory
   - `Compress-Archive` into `spoolsmith-<tag>-windows-amd64.zip`
   - compute a SHA256 file next to it (`Get-FileHash ... -Algorithm SHA256`, written lowercase,
     same format as netviz's `release.yml`)
   - upload both as release assets via the same `actions/github-script@v7` pattern netviz uses
     (resolve the release by tag if not triggered by the `release` event; delete-then-reupload any
     existing asset of the same name so re-running is idempotent)
   - required permission: `permissions: contents: write` at the workflow level, same as netviz.

3. **`scripts/build-release.ps1`** — a plain PowerShell script, runnable manually on any Windows
   machine with Go installed, that performs *exactly* the same steps as the release workflow's
   Windows job: build, stage, zip, hash. Takes an optional `-Tag` parameter (default:
   `dev-<short git commit hash>`) used only in the output filename — this script must not require
   GitHub Actions, a token, or network access; it only needs to run locally. Add a comment at the
   top stating plainly that macOS/Linux equivalents are future roadmap, not built here, per
   DR-0005 §8.

## Explicitly out of scope

- No macOS/Linux build or packaging steps (structure only, per above).
- No code signing — netviz's own product state file records that as unpriced, unresolved
  procurement work; do not invent a signing step here.
- No change to any file outside `.github/workflows/`, `scripts/`, and this spec.
- Do not create a git tag or trigger an actual GitHub Release — that's a separate, later,
  explicit action, not part of writing the workflow files.

## Verification required before reporting done

- `.github/workflows/ci.yml` and `release.yml` must be valid YAML (parse them, don't just write
  them and hope).
- Actually run the equivalent of `scripts/build-release.ps1`'s *logic* on this Linux sandbox using
  cross-compilation (`GOOS=windows GOARCH=amd64 go build -o dist/spoolsmith.exe ./cmd/spoolsmith`)
  to confirm the binary actually cross-compiles cleanly for Windows from this repo — report the
  resulting file size and that the build succeeded. The PowerShell script itself cannot be executed
  on this Linux sandbox; say so plainly rather than claiming it was run.
- Re-run `go build ./...`, `go vet ./...`, `go test ./... -v -count=1` for the native
  (non-cross-compiled) target to confirm nothing in this unit touched or broke the existing code.
