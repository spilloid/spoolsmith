//go:build !windows

package bundle

import "testing"

// trustCurrentUserForTest is a no-op off Windows: checkStageDir already
// accepts directories this user owns.
func trustCurrentUserForTest(t *testing.T) { t.Helper() }
