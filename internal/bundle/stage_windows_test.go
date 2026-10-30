//go:build windows

package bundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// trustCurrentUserForTest lets an unelevated test process own and write a
// stage directory, as SYSTEM or Administrators would in production.
func trustCurrentUserForTest(t *testing.T) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		t.Fatal(err)
	}
	saved := stageTrustees
	stageTrustees = append(append([]*windows.SID{}, saved...), sid)
	t.Cleanup(func() { stageTrustees = saved })
}

func TestCheckStageSecurity(t *testing.T) {
	tests := []struct {
		name string
		sddl string
		want string // "" = accepted
	}{
		{"administrators only", "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", ""},
		{"owned by SYSTEM, users may read", "O:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)", ""},
		{"deny entries only take rights away", "O:BAD:P(D;;FA;;;WD)(A;;FA;;;BA)", ""},
		{"owned by Users", "O:BUD:P(A;OICI;FA;;;SY)", "owned by"},
		{"users may add a file", "O:BAD:P(A;OICI;FA;;;SY)(A;;0x2;;;BU)", "can change it"},
		{"users may add a subdirectory", "O:BAD:P(A;;0x4;;;BU)", "can change it"},
		{"users may delete a child", "O:BAD:P(A;;0x40;;;BU)", "can change it"},
		{"users may rewrite the DACL", "O:BAD:P(A;;WD;;;BU)", "can change it"},
		{"everyone generic all", "O:BAD:P(A;;GA;;;WD)", "can change it"},
		{"creator owner inherit-only", "O:BAD:P(A;OICI;FA;;;BA)(A;OICIIO;FA;;;CO)", "can change it"},
		{"null DACL", "O:BAD:NO_ACCESS_CONTROL", "no DACL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tt.sddl)
			if err != nil {
				t.Fatal(err)
			}
			err = checkStageSecurity(sd)
			if tt.want == "" && err != nil {
				t.Fatalf("checkStageSecurity(%s) = %v, want accepted", tt.sddl, err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("checkStageSecurity(%s) = %v, want an error containing %q", tt.sddl, err, tt.want)
			}
		})
	}
}

func openTestBundle(t *testing.T) *Bundle {
	t.Helper()
	opened, err := Open(writeBundle(t, true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { opened.Close() })
	return opened
}

// A standard user can create folders in C:\Windows\Temp, so a pre-created
// stage path must be refused, and left alone rather than deleted.
func TestEnsureExtractedRefusesAStageAStandardUserCouldWrite(t *testing.T) {
	opened := openTestBundle(t)
	dest := filepath.Join(t.TempDir(), "stage")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(dest, "brhl2315a.inf")
	if err := os.WriteFile(planted, []byte("planted"), 0600); err != nil {
		t.Fatal(err)
	}
	err := opened.EnsureExtracted(dest)
	if err == nil || !strings.Contains(err.Error(), "was not created by an administrator") {
		t.Fatalf("EnsureExtracted() over a user-created directory = %v, want a refusal", err)
	}
	if _, statErr := os.Stat(planted); statErr != nil {
		t.Fatalf("the refused directory was modified: %v", statErr)
	}
}

func TestEnsureExtractedRefusesAJunction(t *testing.T) {
	trustCurrentUserForTest(t)
	opened := openTestBundle(t)
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "stage")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", dest, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v %s", err, out)
	}
	err := opened.EnsureExtracted(dest)
	if err == nil || !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("EnsureExtracted() through a junction = %v, want a refusal", err)
	}
}

func TestEnsureExtractedCreatesAProtectedTrusteesOnlyDirectory(t *testing.T) {
	trustCurrentUserForTest(t)
	opened := openTestBundle(t)
	dest := filepath.Join(t.TempDir(), "stage")
	if err := opened.EnsureExtracted(dest); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dest, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("stage directory DACL inherits from its parent; want it protected")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !stageTrustee(sid) {
			t.Fatalf("stage directory grants %s", sid)
		}
	}
	// The same check a later run applies before reuse must accept it.
	if err := checkStageDir(dest); err != nil {
		t.Fatalf("checkStageDir() on SpoolSmith's own stage = %v", err)
	}
}
