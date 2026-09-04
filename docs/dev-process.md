# Dev Process Log

Records every round where implementation or review work is routed to Codex CLI (`codex exec`,
tiers Luna/Terra/Sol), so the routing policy below stays derived from measurements on *this* repo
rather than imported wholesale from another project. The tiering itself and the
orchestrator-drives/Codex-implements-and-reviews discipline are adapted from the pattern already
proven on player-2 (`docs/DEV-PROCESS.md`), PartnerCenterBridge, and AnchorDesk
(`docs/dev-process.md`) — see `corporate-strategy/standards/STD-001-adversarial-review.md` for the
company-wide history and its own honest caveats about how proven this pattern actually is. Claude
orchestrates (writes the spec/contract first, decides what to dispatch and at what tier, adjudicates
every finding itself); Codex CLI implements and reviews. **Adopted at bootstrap, before any code
exists, per DR-0005 §9** (`corporate-strategy/board/meetings/2026-09-03-spoolsmith-new-product/04-decision-record.md`)
— this is a deliberate, disclosed deviation from every other adopting repo's history, where the
routing table was written after real incidents already existed to derive rules from. This file
starts empty of log rows and accrues real entries from here forward.

## Routing

- **Luna** — mechanical, tightly-specified work: an established pattern to mirror, tests or an
  exact shape already given. Never shipped unreviewed.
- **Terra** — default implementer and default adversarial reviewer of anything Luna or the
  orchestrator wrote.
- **Terra/high** — escalation: nontrivial logic, more than a couple files, anything Terra/medium
  would be guessing on.
- **Sol/high** — architecture, security, concurrency, and anything touching:
  - privileged local execution, elevation, or a driver-store/spooler write of any kind
  - fetching a remote payload and executing or staging it, under any flag
  - the NetViz integration boundary (`CLAUDE.md`'s "NetViz integration boundary" section) —
    changing what NetViz may invoke or what evidence shape crosses that boundary

  regardless of diff size. **This is written in now, before any code exists that would trip it —
  per Seat 03 (Security)'s condition in the SpoolSmith board debate, specifically to avoid writing
  the rule under pressure from a diff someone already spent hours on.**
- **Sol/ultra** — multi-file (5+) implementation or a broad review pass. As implementer, requires
  the operator's explicit in-session approval; never self-initiated.

These are starting priors adopted from the company-wide pattern, not derived from this repo's own
incidents yet — revise whichever rule the log below stops supporting, and say so when it happens.

## Standing rules carried over

None yet — this repo has no incident history. New rules get added here the same way every
adopting repo has done it: after a real defect, not speculatively. The one rule adopted in advance
rather than after an incident is the Sol/high trigger above, and it's flagged as such rather than
presented as if it came from this repo's own history.

## Log

| Unit | Task type | Author | Reviewer | Defects found | Defects real | Caught by tests instead | Est. tokens | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
