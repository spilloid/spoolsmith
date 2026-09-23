# CLAUDE.md

## Project Mission

**Operator update, 2026-09-22 (later same day):** there is exactly one on-disk
printer-file format now: a bundle (package `internal/bundle`, extension
`.ssb`), optionally carrying a driver payload. The bare-JSON profile format
`profile capture`/`install --profile` used to write and read is gone —
`internal/install.Profile` is now a pure in-memory struct with no file I/O of
its own; `internal/bundle.SaveProfile`/`LoadProfile`/`EditProfile` are the one
place a profile is ever written to or read from disk, always as a
zero-or-more-driver-file bundle. This was the operator's own standing
complaint made concrete: a saved profile and a handed-off bundle were the same
document in two different file shapes, for no stated reason.
- `profile capture`, `copy`, `install`/`add`/`configure`/`remove --profile`,
  `apply`, `status --profile`, `intune build --profile`, and the desktop
  GUI's saved-setup/copy/apply/Intune pickers all read and write `.ssb` now.
  `install --profile`/`add --profile` gained `apply`'s embedded-driver-payload
  staging in the process, since the two are now the same file shape end to
  end — they share one loader (`loadProfileFile` in `cmd/spoolsmith`,
  `loadPrinterFile` in `cmd/spoolsmith-gui`).
- `internal/intune`'s `loadProfileSource` no longer dispatches on extension
  (there is nothing left to dispatch on); a bundle naming a local
  `driver_package` vendor archive is now accepted as long as it carries no
  *embedded* payload of its own — that dual-mechanism conflict remains
  refused, the old blanket "bundle can never name a vendor archive" rule
  does not.
- The "saved setups" bulk transfer (`profile export-all`/`import-all`, the
  desktop GUI's Export/Import all) is now a **set** — package
  `internal/bundle`'s `WriteSet`/`OpenSet`/`SetMember` — a zip carrying the
  member `.ssb` files verbatim plus a small `set.json` index, not a bespoke
  JSON document re-encoding every profile inline. A member carrying an
  embedded driver payload is refused outright (settings-only transfer, always
  was) rather than silently carried or silently dropped.
- `examples/intune/accounting.json` is gone; `examples/intune/accounting.ssb`
  is the one example file, and the README shows the schema inline as
  documentation instead of shipping a second, now-unusable format.
- Deliberately untouched: evidence fixtures (`fixtures/*.json`, milestone-one
  testing) are a different, internal-only format and were never part of this
  duality — an operator never hands one between machines. `copy --all` still
  writes a folder of `.ssb` files rather than a set; unifying that (and giving
  `apply` a set's per-member plan-and-confirm loop) is a deliberately separate,
  not-yet-decided follow-up, not an oversight.
- See `internal/bundle/profile.go` and `internal/bundle/set.go` for the exact
  mechanics, and their tests (plus `internal/profileset`'s rewritten suite)
  for the boundary between what's safe to condense and what still fails
  closed (an embedded-driver-payload conflict, an unsafe or duplicate member
  name, a set whose index disagrees with its own archive contents).

**Operator update, 2026-09-22:** offline fallback is now first-class across
`install`/`apply`/`copy`, on both the CLI and desktop, instead of a manual
`--offline` flag the operator had to already know to reach for. A printer
that won't answer right now — still booting after a physical move, a cable
not yet seated, DHCP settling on a new subnet — no longer fails the whole
operation outright:
- `install --profile` / `apply <bundle>`: if the live probe can't reach the
  printer, or the printer answers but never confirms identity (even after the
  existing one-retry-for-a-sleeping-printer path), the run now falls back to
  the same offline path `--offline` already supported, and says so on the
  transcript (`Note: offline fallback: ...`) and in `Outcome.Resolution`
  (`offline-fallback-operator-profile`, distinct from an operator-requested
  `offline-operator-profile`). A profile with no captured identity at all is
  never retried against a live probe it can't possibly match. This never
  skips the one required plan confirmation — see the trust model below.
- `copy` (rip a bundle off an already-installed queue): if the source printer
  never answers after the existing retry, `copy` no longer fails the whole
  operation. It writes the bundle anyway from what Windows already knows
  about the queue (name/driver/address), with the profile's evidence marked
  `"provenance": "unconfirmed"` and a note explaining why. `copy --all`
  reports this per queue rather than failing the batch. Applying such a
  bundle always runs the offline path above — there is nothing in it to ever
  confirm against a live printer. A canceled operation is still a hard
  failure; only "the printer never answered" is degraded.
- See `internal/install/workflow.go` (RunInstall), `internal/bundle/create.go`
  (collectIdentity/Create) and their tests for the exact boundary between
  "degrade to offline" and "still fail closed" — an identity *mismatch*
  (saved vs. observed) is a conflict, never treated as unreachable, and always
  still fails outright.

**Operator update, 2026-09-17 (v0.7.0 track):** the desktop GUI's Intune wizard
("Build an Intune printer app..." on the Tools tab) is now enabled — it calls the
same `internal/intune` `Prepare`/`Export` the CLI's `intune build`/`wizard` uses,
so it's the same local-only packaging surface, not new scope. Real Windows
validation is in `docs/validation/2026-09-17-gui-intune-wizard.md`. Tenant
sign-in and automatic upload/group-creation/assignment remain explicitly **not**
planned for either surface.

**Operator update, 2026-09-17 (later same day):** the operator scoped Intune
packaging as: local generation of the Win32 app content (install/uninstall/detect
scripts, README with exact commands) is supported product surface, released via
`spoolsmith intune build`/`wizard` on the CLI. Tenant sign-in and automatic
upload/group-creation/assignment are explicitly **not** planned — "we're a printer
deployment enabler," not a tenant-management tool — and stay out of scope unless a
future decision changes that. See `docs/roadmap.md` and `docs/intune-deployment.md`
for the current supported workflow and what's still manual.

**Operator update, 2026-09-17:** v0.6.0 is released with native desktop workflow
parity, printer copy/apply, offline profiles and bulk JSON transfer. The operator
requested a post-release quality, UX and product-site pass. Use `README.md`,
`docs/roadmap.md` and dated `docs/validation/` records for current behavior and gaps;
the milestone-one scope below is historical. Keep reviewed-plan confirmation and
local driver trust checks. Automatic driver downloads remain future work.

**Operator update, 2026-09-06:** The operator asked Astra to prioritize daily use:
known-IP mapping, discovery, and reusable per-printer JSON. See
`docs/daily-use-spec.md`. Explicit operator profiles now extend queue mapping beyond
the two built-in catalog families, using an already-registered Windows driver and
fresh evidence checks. The historical two-family restrictions below still describe
automatic catalog selection and driver-package work; they do not prohibit this
operator-authorized profile mapping. The reviewed Brother local-archive recipe now
supports staging after the same plan confirmation. Downloads remain future work.

SpoolSmith identifies network printers, resolves the correct OEM driver through a small,
family-oriented catalog, presents a reviewable install plan, and installs/configures the printer
locally only after explicit user approval. It is a standalone product with its own CLI, its own
driver catalog, and its own OS-mutation logic — not a NetViz feature, not an RMM.

Governance: authorized by `corporate-strategy` board decision DR-0005
(`board/meetings/2026-09-03-spoolsmith-new-product/04-decision-record.md`,
`decisions/DECISION_LOG.md` D-0039) as a **milestone-one authorization, not a product
authorization**. See `corporate-strategy/state/products/SpoolSmith.md` for current status,
budget, and revisit triggers before assuming any scope beyond what's written here.

## Current scope: v0.1.0 — live detection + approved install for two named families

Milestone one (fixture-only detection) is done. Per corporate-strategy D-0040
(`decisions/DECISION_LOG.md`), OS mutation is now authorized, but **narrowly**:

1. Collect real fingerprint evidence live from the network (SNMP/HTTP/PJL/ports/OUI/hostname), or
   from a fixture file for testing.
2. Normalize evidence, resolve a printer family, then a driver package/strategy — unchanged,
   already fail-closed, already reviewed.
3. Emit a deterministic `inspect`/install-plan result — unchanged.
4. **For exactly two named families (HP LaserJet Pro M4xx; Brother HL-L2xxx), and only via the
   documented trust model in D-0040, install the driver and configure the TCP/IP printer port —
   after showing the full plan and receiving one explicit confirmation.** Detection and resolution
   run automatically; installation does not skip the confirmation step under any flag.

**Still no remote execution beyond SpoolSmith's own local fingerprinting/install commands, no
credential collection, and no network auto-fetch of driver packages** (D-0040's trust model is
explicit that installers must already be staged locally or resolved through Windows' own
`Add-PrinterDriver`/`pnputil` path — SpoolSmith does not itself download an installer from a URL).

Catalog hierarchy, deliberately layered so a giant hand-maintained per-model database is never the
shape of this system:

```
observed identifiers -> normalized model -> printer family -> driver package/strategy
```

Many model aliases should map to relatively few driver families. Driver package metadata (URLs,
versions, hashes, signatures — volatile OEM information) must stay a separate, swappable layer
from the family catalog (stable device-identity mappings) — see Architecture Rules below.

Definition of done for milestone one (DR-0005 §6, non-negotiable):
- The fixture set states explicitly which fingerprint evidence it assumes vs. what's actually
  obtainable from real hardware.
- At least one fixture per some claimed family is a **captured** real-device observation, not
  only synthetic.
- At least one ambiguous/unsupported-device case exists and resolves to a **fail-closed** result —
  never something that could be mistaken for an approved install plan.
- Golden-output tests are deterministic (same evidence in, same plan out, byte-for-byte).
- Evidence provenance (where each fingerprint field came from) is visible in the output, not
  collapsed away.

## What Not To Build Yet

Originally written in at bootstrap per DR-0005 §3 and Seat 03's (Security) condition; **narrowed,
not deleted, by D-0040** (`corporate-strategy/decisions/DECISION_LOG.md`) once OS mutation was
authorized for the two named families:

- No OS mutation, driver-store write, elevation, or privileged process-exec for anything **outside
  the two D-0040-named families or outside its documented trust model** — not without its own
  separate future board/operator motion, regardless of how small the diff looks. This still
  includes: any additional family, any install mechanism other than the documented one, and any
  path that skips the required confirmation step.
- No fetching-and-executing a remote payload — D-0040's trust model explicitly excludes SpoolSmith
  downloading an installer from a network URL itself. Any change to add that is a new decision.
- No remote shell, no remote command execution beyond SpoolSmith's own local fingerprinting and
  its documented, visible install commands (PowerShell cmdlets / vendor's own signed installer).
- No credential handling or credential storage of any kind.
- No RMM-like workflows — SpoolSmith installs a driver a human already reviewed and approved,
  nothing else, and never on a schedule or in response to anything but an explicit, one-time
  confirmed command.
- **No fuzzy match ever installs anything silently, and no install ever skips the one required
  confirmation of a shown plan — this line survived D-0040 unchanged and is not up for
  negotiation by a future diff.** Confidence and evidence are always inspectable.
- No per-model hand-maintained catalog sprawl — if a change looks like "add model #500 to a giant
  table," the family/catalog abstraction has failed and that's a finding, not a feature.

## Driver-payload trust model (D-0040 — required to exist before install code, not after)

- Source: vendor-published installers only (HP UPD, Brother Full Driver Package). No mirrors.
- No network auto-fetch — installer must be staged locally or resolved via Windows'
  `Add-PrinterDriver`/`pnputil` against Windows Update/inbox drivers.
- Verification: Windows' own Authenticode signature check on the vendor EXE/MSI; `DriverPackage.SHA256`
  checked against the staged file as defense-in-depth when populated.
- Elevation: SpoolSmith does not self-elevate; fails closed if not run as Administrator.
- Approval: exactly one explicit confirmation of the full shown plan before any mutation, always.
- Rollback: `uninstall` reverses exactly what `install` recorded.

## Architecture Rules

- Go core, CLI first. Optional desktop surface is future roadmap, not current scope.
- Detection/normalization/catalog-resolution logic is pure and OS-independent; Windows
  install/mutation behavior lives behind clean, narrow OS-specific interfaces so the core is
  testable without Windows and without real hardware.
- Driver package/version/hash/signature metadata is a separate, explicitly swappable layer from
  the family catalog — volatile OEM download details must never leak into the stable
  identifier→family→catalog mappings.
- Evidence sources (SNMP, HTTP device-UI/model strings, PJL/JetDirect, open printing ports,
  MAC/OUI, hostname hints) are independent probes feeding one evidence-aggregation step; no probe
  should be load-bearing on its own for a driver decision without being visible in the output.

## NetViz integration boundary (DR-0005 §4 — read before adding any NetViz-facing code)

NetViz may hand SpoolSmith `HostObservation`-shaped evidence (IP, hostname, MAC/OUI vendor, open
ports, device-type guess) **and** invoke SpoolSmith for a **read-only preview/plan request only**.
SpoolSmith must never expose an invocation surface to NetViz (or anything else) that mutates
anything, locally or remotely — that stays gated exclusively by the OS-mutation prohibition above,
regardless of what calls it. SpoolSmith does not depend on NetViz existing; it gathers its own
fingerprinting evidence independently and must remain fully useful from its own CLI with zero
NetViz integration wired up.

## Product icon

`assets/icon/spoolsmith.png` is the single master (square, at least 256px, transparent
background). Everything else is generated from it and committed: the icon inside both
executables (`rsrc_windows_amd64.syso` in `cmd/spoolsmith` and `cmd/spoolsmith-gui`) and the
site's `favicon.ico`, touch icon and header PNGs. To change the artwork, replace that one file
and run:

```sh
go generate ./cmd/spoolsmith ./cmd/spoolsmith-gui ./internal/icon
```

Drift tests fail until you do. The icon group is resource ID 7 on purpose — tailscale/walk
loads exactly that ID for every window class it registers, so the main window and all dialogs
get the icon with no GUI code. Don't renumber it.

## Release checklist

A release is not just a version bump. Every time `VERSION` moves and a `releases/vX.Y.Z.md`
is written, also check whether the GUI changed since the last release and, if so:
- Refresh `docs/img/*.png` for any screen that changed (button/label text, layout, new controls)
  — `SiteScreenshots.cs`'s `Capture_site_screenshots` test does this live against the real app
  (`SPOOLSMITH_CAPTURE_SITE_SHOTS=1 dotnet test --filter FullyQualifiedName~SiteScreenshots`,
  real Windows only). A feature that's real but never screenshotted (Intune packaging shipped in
  v0.7.0, first screenshotted in v0.7.3) is exactly the gap this step exists to catch.
- Check `docs/index.html`'s gallery and copy against what the screenshots actually show now —
  a stale caption next to a fresh screenshot is its own kind of drift.
This was missed for two releases in a row (v0.7.0 shipped Intune with no screenshot; v0.7.1's own
site-update commit didn't add one either) before being caught and fixed in v0.7.3.

Releases are Authenticode-signed by the release workflow, which fails rather than publishing
unsigned binaries — see `docs/code-signing.md`. After a release publishes, download the actual
asset and confirm `Get-AuthenticodeSignature` reports `Valid` on both EXEs. CI verifies what it
built; this verifies what users actually get.

## Commands

```sh
go build ./...
go vet ./...
go test ./... -v
go run ./cmd/spoolsmith inspect fixtures/hp-laserjet-m404-synthetic.json
```

## Coding Conventions

- Prefer small, explicit Go packages, mirroring netviz's own convention (`internal/scanner`
  isolation, typed events over callbacks).
- Every exported catalog-resolution function must be a pure function over its inputs — no hidden
  network calls, no hidden filesystem reads — so milestone one's acceptance tests can run
  anywhere, including CI, with zero real hardware.
- Add tests for: evidence normalization, ambiguous-match fail-closed behavior, and deterministic
  golden-output comparison, at minimum.
