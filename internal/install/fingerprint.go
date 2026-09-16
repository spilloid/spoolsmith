package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// FingerprintPlan returns a stable SHA-256 over the exact plan an operator was
// shown, including every command. It exists so a reviewed plan can be named in
// a script: the operator reviews one plan by hand, then the loop that maps the
// remaining machines passes that fingerprint, and any machine whose computed
// plan differs in any respect fails closed instead of mutating.
//
// This is a narrower authorization than --yes, not a broader one. --yes accepts
// whatever plan is computed on that machine, sight unseen. A fingerprint
// accepts exactly one plan and rejects everything else.
//
// Plan contains no maps, so encoding/json emits its fields in declaration order
// and the digest is reproducible across machines and builds.
func FingerprintPlan(plan Plan) (string, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("install: fingerprint plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
