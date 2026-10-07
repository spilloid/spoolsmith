# Fix spec — internal/probe adversarial review findings

Found by independent Codex review (`-s read-only -c model_reasoning_effort=high`), reproduced or
directly confirmed by the orchestrator before authorizing this fix (BER length-wrap doesn't
manifest on this repo's actual 64-bit build targets, but the underlying arithmetic defect is real
and confirmed by direct code reading; the predictable request ID is confirmed as literally
`time.Now().UnixNano() & 0x7fffffff`, exactly as reported). No Critical/High findings. Fix all
four; add the missing concurrency test as its own item.

## 1. SNMP request ID is predictable (Medium)

`probeSNMP` in `snmp.go` derives the request ID from wall-clock time, which repeats/is guessable
within a ~2.1 second window. SNMPv1 over UDP has no transport authentication regardless — a
well-positioned on-path or same-subnet attacker can still forge a response with the right
community string even with a perfectly random ID. **Fix what's cheaply fixable, disclose what
isn't:** generate the request ID with `math/rand/v2` (or `crypto/rand` truncated to 31 bits) seeded
properly, not from time. Add a one-line comment noting that this closes the "trivially predictable
from wall-clock" weakness but does not and cannot make SNMPv1 authenticated — that's inherent to
the protocol, not a defect specific to this implementation, and downstream evidence still flows
through `catalog.Resolve`'s existing fail-closed logic before anything gets shown to a human, let
alone installed.

## 2. BER long-form length can integer-overflow on a 32-bit `int` (Medium)

`readLength` in `ber.go` accumulates up to 4 length octets into a plain `int` with no upper bound
or sign check before the caller slices `r.data[r.pos : r.pos+length]`. On a 32-bit build this can
wrap negative and produce a slice expression that panics (confirmed by direct arithmetic; does not
reproduce on this repo's actual amd64 targets, but the defect is real and this package should not
depend on `int` happening to be 64 bits). **Fix:** accumulate in `uint64`, reject any decoded
length that exceeds a sane fixed bound (this package never needs a length anywhere close to the
8KB UDP packet cap — reject anything over, say, 65535 outright) before ever converting to `int`.
Add a test with the exact byte sequence from the review (`30 84 ff ff ff ff`) asserting a clean
error, never a panic, regardless of platform word size.

## 3. Cold ARP cache makes OUI lookup fail nondeterministically on a fresh probe (Low)

`probeOUI` in `oui.go` reads `/proc/net/arp` once, immediately, concurrently with the other probes
— on a genuinely cold neighbor cache the entry may not exist yet even though the concurrent
TCP/UDP probes are about to trigger ARP resolution as a side effect. **Fix:** before reading the
ARP cache, attempt a cheap operation that reliably triggers ARP resolution for the target
(e.g. a short-timeout `net.Dial("udp", ...)` to any port on the target — this doesn't need to
succeed at the application layer, just cause the kernel to resolve the neighbor), then retry the
ARP cache read up to 2 times with a short delay (e.g. 150ms) if the first read doesn't find the
entry. Keep the whole OUI probe's total bound well under the probe's overall timeout budget.

## 4. BER decoder accepts non-canonical OID encoding and doesn't enforce expected value types (Low)

Two related gaps in `ber.go`/`snmp.go`:
- `decodeOID` accepts a non-minimal subidentifier (e.g. a leading `0x80` continuation byte
  encoding a redundant/zero leading digit) that X.690 §8.19 requires be rejected.
- `decodeSNMPGetResponse` accepts either OCTET STRING (`0x04`) or OBJECT IDENTIFIER (`0x06`) for
  *any* variable binding, rather than requiring `sysDescr` specifically be an OCTET STRING and
  `sysObjectID` specifically be an OBJECT IDENTIFIER.

**Fix:** in `decodeOID`, reject a subidentifier whose first byte is `0x80` (non-minimal encoding).
In `decodeSNMPGetResponse`, look up which OID each binding is for and require the specific expected
tag for that OID (`0x04` for sysDescr, `0x06` for sysObjectID); reject anything else as malformed
rather than accepting it permissively.

## 5. Add the concurrency test the review correctly flagged as missing

The existing `probe_test.go` only calls `Collect` with an empty target (returns before any
goroutine starts) — even a clean `-race` run of the existing suite doesn't exercise the real
concurrent-write path. The orchestrator already confirmed directly (5x, `-race`, against
`127.0.0.1` with nothing listening) that this path is genuinely race-free, but that check needs to
be a permanent test, not a one-off scratch run. **Add** a test calling `Collect(ctx, "127.0.0.1")`
(nothing needs to be listening — every probe will fail fast/timeout, which still exercises every
goroutine's write into the shared `Result`) and run the whole package under `-race` as part of
verification.

## Verification required before reporting done

- `go build ./...`, `go vet ./...` clean.
- `go test ./... -v -count=1` **and** `go test ./internal/probe/... -race -count=5` both clean —
  paste the actual output for both, not just the non-race run.
- New tests for all four numbered findings above, each asserting the fixed behavior specifically
  (not just "still passes existing tests").
- All of milestone one's existing tests (catalog/evidence/inspect) still pass unchanged.
