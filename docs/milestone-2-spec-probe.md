# Milestone 2, Unit 1 — Live evidence collection (`internal/probe`)

Written by the orchestrator before dispatch, per STD-001. Foundation for the rest of v0.1.0 — see
`corporate-strategy/decisions/DECISION_LOG.md` D-0040 for the governance authorization this
milestone operates under (this unit itself performs no OS mutation and needs none of D-0040's
authorization — it is read-only network I/O, same risk class as milestone one's fixture reader).

## Goal

Given an IP or hostname, collect real printer fingerprint evidence over the network and assemble
it into the existing `evidence.Evidence` struct with `Provenance: "captured"`. No fabrication, no
guessing at values — every field is either a real observed value or left empty.

## Hard constraints

- **No new third-party Go dependencies.** `go.mod` currently has zero `require` lines; keep it
  that way. SNMP GET-only support is small enough to hand-roll (see below) rather than importing a
  library — this repo's own trust-model discipline (D-0040) argues against adding supply-chain
  surface casually, and that applies to build-time dependencies too, not just runtime driver
  fetches.
- **Read-only.** No SNMP SET, no writes of any kind, no credential prompts, no stored credentials
  (SNMP community string is always the literal `public`, never configurable via a flag that could
  be mistaken for a credential input in this unit).
- **Bounded and defensive.** Every network operation gets a short context timeout (2-3 seconds is
  reasonable per probe). Parsing SNMP/PJL/HTTP responses from a real device is parsing untrusted
  input — bounded reads, no unbounded buffers, no panics on malformed responses (return an error
  for that one probe, don't crash the whole collection).
- **Concurrent, not sequential.** Run the independent probes (SNMP, HTTP, PJL, port checks,
  hostname) concurrently with `errgroup` or a plain `sync.WaitGroup`, not one-at-a-time — this
  should take roughly as long as the slowest single probe's timeout, not the sum of all of them.

## Package: `internal/probe`

```go
package probe

// Result is what Collect returns: the assembled evidence plus a transparent
// record of which sub-probes succeeded or failed and why. Errors here are not
// fatal to Collect as a whole — a device with SNMP disabled and PJL working
// should still produce useful evidence.
type Result struct {
    Evidence evidence.Evidence
    Probes   []ProbeOutcome
}

type ProbeOutcome struct {
    Name     string // "snmp", "http", "pjl", "ports", "oui", "hostname"
    Success  bool
    Detail   string // what was found, or the error, human-readable
    Duration time.Duration
}

// Collect gathers evidence from ip (an IP address or hostname) over the
// network. It always returns a Result — total failure of every probe is not
// itself an error from Collect's signature; the caller inspects Probes to see
// what happened. Collect returns a non-nil error only for a fatal
// precondition (e.g. ip fails to resolve at all).
func Collect(ctx context.Context, ip string) (Result, error)
```

### SNMP (`internal/probe/snmp.go`)

Hand-rolled SNMP v1 GET-Request/GET-Response over UDP port 161, community `public`, for exactly
two OIDs: `1.3.6.1.2.1.1.1.0` (sysDescr) and `1.3.6.1.2.1.1.2.0` (sysObjectID). No walk, no v2c/v3,
no SET. This is BER/ASN.1-encoded; write a minimal encoder for just the PDU shapes this needs
(INTEGER, OCTET STRING, OBJECT IDENTIFIER, SEQUENCE, and the SNMP message/PDU wrapper) rather than
pulling in a general ASN.1 or SNMP library. Cite the SNMP v1 RFC (RFC 1157) PDU structure in a
comment for whoever maintains this next. Timeout ~2s, one retry on timeout, then give up and
record the failure in `ProbeOutcome`.

### HTTP (`internal/probe/http.go`)

`http.Client` with a ~2s timeout, GET `http://<ip>/` (try plain HTTP only in this unit — no TLS
cert handling complexity for v0.1.0). Extract `<title>...</title>` via a simple string search (not
a full HTML parser — this is a best-effort scrape, not a robust one) into `HTTPTitle`. Do not
follow more than a couple of redirects. Cap the response body read at something small (e.g. 64KB)
— printer embedded web servers are small pages, and this bounds a malicious/broken device from
forcing an unbounded read.

### PJL (`internal/probe/pjl.go`)

Raw TCP connect to port 9100, ~2s timeout. Write the PJL INFO ID request exactly as HP's PJL
Technical Reference Manual documents it (already cited in `fixtures/hp-laserjet-m404-synthetic.json`'s
provenance note): a Universal Exit Language wrapper (`<ESC>%-12345X`) around `@PJL INFO ID\r\n`.
Read the response with a bound on total bytes (a few KB is plenty), and parse the quoted model
string out of the `@PJL INFO ID\r\n"<model>"\r\n<FF>` response shape. If the device doesn't speak
PJL at all (connection refused/reset, or a response that doesn't match the expected shape), record
that as a failed probe, not a parse panic.

### Ports (`internal/probe/ports.go`)

TCP connect-scan (no SYN, no raw sockets — plain `net.DialTimeout`, mirroring netviz's own
TCP-connect-only philosophy) against exactly: 80, 443, 9100, 631. ~1s timeout each, run
concurrently with each other too. Populate `Evidence.OpenPorts`.

### OUI (`internal/probe/oui.go`)

A small, static, embedded table mapping MAC OUI prefixes (first 3 octets) to vendor names, sourced
from the public IEEE OUI registry — **cite the actual source and only include prefixes you can
verify are real** (e.g. via publicly documented OUI lookup data), rather than inventing plausible-
looking hex prefixes. Include entries for at least HP and Brother (the two currently-supported
families); a handful more (Canon, Epson, Lexmark) is fine if verified, but this is explicitly not
meant to be exhaustive — it's a hint feeding `MACVendor`, not a source of truth. To get the actual
MAC address for a given IP, read it from the local ARP table/cache (Linux: parse `/proc/net/arp`;
this unit's CI target is Linux, and Windows-specific ARP reading can be a documented follow-up
inside the same file behind a small OS-conditional if easy, but do not block this unit on it if
Windows ARP reading needs meaningfully different code — note in `docs/dev-process.md` if skipped).

### Hostname (`internal/probe/hostname.go`)

Best-effort reverse DNS (`net.DefaultResolver.LookupAddr`) with a short timeout. Never fatal.

## Assembly

`Collect` runs all five probes concurrently, populates `evidence.Evidence` with whatever was
found, sets `Provenance: "captured"` (no `provenance_note` required for `"captured"` — matches
`evidence.LoadFixture`'s existing validation, which only requires a note for `"synthetic"`), and
returns a `Result` whose `Probes` field records exactly what happened per probe — this is what
makes `catalog probe` (below) useful for debugging a device that doesn't resolve.

## CLI changes (`cmd/spoolsmith/main.go`)

Move to real subcommand dispatch (this repo currently only recognizes `inspect <fixture>`):

- `spoolsmith inspect <target>` — if `target` is an existing local file (`os.Stat` succeeds and
  it's a regular file), load it via the existing `evidence.LoadFixture` path (unchanged, all
  current tests depend on this). Otherwise, treat `target` as an IP/hostname and call
  `probe.Collect`. Either way, run the existing `inspect.Inspect` and print the same JSON shape as
  today — this unit does not change `InspectResult`'s shape, only how `Evidence` gets populated.
- `spoolsmith catalog probe <ip>` — calls `probe.Collect` directly and prints the full `Result`
  (evidence + per-probe outcomes) as indented JSON. This is the "make adding a new family cheap"
  developer workflow from the original product ask; it does not run catalog resolution at all,
  just shows raw evidence.

## Explicitly out of scope for this unit

- Anything in `internal/install` (that's Unit 2, gated separately, not started until this unit is
  verified).
- SNMP v2c/v3, SNMP walk, any SNMP SET.
- HTTPS/TLS handling, following more than 2 redirects, scraping more than the page title.
- Windows-native ARP reading if it turns out to need materially different code than the Linux
  `/proc/net/arp` path — note as a follow-up rather than blocking on it.
- Any change to `internal/catalog` or `internal/evidence`'s existing types (only `Collect` adds
  new evidence; the shapes stay as milestone one left them, already reviewed and fixed).

## Verification required before reporting done

- `go build ./...`, `go vet ./...`, `go test ./... -v -count=1` clean, including new tests for:
  SNMP encode/decode round-trip against a known-good captured byte sequence (construct one by hand
  from the RFC 1157 PDU shape, don't just test against your own encoder in a circular way), PJL
  response parsing against both a well-formed and a malformed/truncated response, HTTP title
  extraction, and the OUI table's cited entries.
- All existing tests (from milestone one) must still pass unchanged.
- Report explicitly: which probes were actually exercised against something real vs. only
  unit-tested against constructed fixtures (this sandbox likely has no real printer reachable — say
  so plainly, don't imply live-network verification happened if it didn't).
