package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewHandOverRoundTripsAndIsRevalidated(t *testing.T) {
	op := operation{Kind: opApply, BundlePath: `C:\files\Front desk.ssb`, PrinterName: "Front", Offline: true, PurgeDriver: true}
	arg, err := encodeReview(op)
	if err != nil {
		t.Fatal(err)
	}
	request, err := parseLaunchArgs([]string{arg})
	if err != nil {
		t.Fatal(err)
	}
	got := request.Review
	if got == nil || got.Kind != opApply || got.BundlePath != op.BundlePath || !got.Offline {
		t.Fatalf("review = %+v", got)
	}
	if got.PurgeDriver {
		t.Fatal("an option that does not apply to this kind survived the hand-over")
	}
	if _, err := parseLaunchArgs([]string{resumeFlag + "not base64!"}); err == nil {
		t.Fatal("garbage hand-over accepted")
	}
	empty, _ := encodeReview(operation{Kind: opApply})
	if _, err := parseLaunchArgs([]string{empty}); err == nil {
		t.Fatal("an operation that cannot be reviewed was accepted")
	}
}

func TestLaunchArgsCollectFilesAndIgnoreSwitches(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "b.zip")
	request, err := parseLaunchArgs([]string{"-Embedding", "a.ssb", "", abs})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Files) != 2 || !filepath.IsAbs(request.Files[0]) || request.Files[1] != abs {
		t.Fatalf("files = %v", request.Files)
	}
}

func TestQuoteArgSurvivesSpacesQuotesAndTrailingSlashes(t *testing.T) {
	cases := map[string]string{
		`plain`:        `plain`,
		`C:\a b\c.ssb`: `"C:\a b\c.ssb"`,
		`C:\dir with\`: `"C:\dir with\\"`,
		`say "hi"`:     `"say \"hi\""`,
		``:             `""`,
	}
	for in, want := range cases {
		if got := quoteArg(in); got != want {
			t.Errorf("quoteArg(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestAssociationCommandRoundTrips(t *testing.T) {
	exe := `C:\Tools\SpoolSmith 1.3\spoolsmith-gui.exe`
	if got := associationTarget(associationCommand(exe)); got != exe {
		t.Fatalf("associationTarget = %q", got)
	}
	if got := associationTarget(`C:\x.exe "%1"`); got != `C:\x.exe` {
		t.Fatalf("unquoted = %q", got)
	}
}

func TestClipboardFoldersAreNamedAndExpired(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	name := clipboardFolderName(now)
	if !strings.HasPrefix(name, "copy-") {
		t.Fatalf("name = %q", name)
	}
	if staleClipboardFolder(name, now.Add(-time.Hour), now) {
		t.Error("a fresh copy was treated as stale")
	}
	if !staleClipboardFolder(name, now.Add(-25*time.Hour), now) {
		t.Error("a day-old copy was kept")
	}
	if staleClipboardFolder("someone-else", now.Add(-48*time.Hour), now) {
		t.Error("a folder we did not make was treated as ours")
	}
}

func TestPrinterFileCandidatesDropFoldersAndOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.ssb", "b.ZIP", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, rejected := printerFileCandidates([]string{filepath.Join(dir, "a.ssb"), filepath.Join(dir, "b.ZIP"), filepath.Join(dir, "c.txt"), dir})
	if len(got) != 2 || len(rejected) != 2 {
		t.Fatalf("candidates = %v, rejected = %v", got, rejected)
	}
}
