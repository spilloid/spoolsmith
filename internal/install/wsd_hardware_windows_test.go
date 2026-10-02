//go:build windows

package install

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run only when explicitly pointed at an installed WSD queue. This is a
// read-only hardware check; ordinary CI has no printer or multicast network.
func TestHardwareWSDConversion(t *testing.T) {
	queue := os.Getenv("SPOOLSMITH_TEST_WSD_QUEUE")
	if queue == "" {
		t.Skip("set SPOOLSMITH_TEST_WSD_QUEUE to a real WSD queue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	env := NewEnvironment()
	cloned, err := CloneQueue(ctx, env, queue)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.SourceWSDPort == "" || cloned.SourceWSDDeviceID == "" || cloned.HostAddress == "" || cloned.USB {
		t.Fatalf("invalid WSD conversion: %+v", cloned)
	}
	t.Logf("WSD %s (%s) -> %s at %s", cloned.SourceWSDPort, cloned.SourceWSDDeviceID, map[bool]string{true: "IPP", false: "RAW 9100"}[cloned.IPPURL != ""], cloned.HostAddress)
}
