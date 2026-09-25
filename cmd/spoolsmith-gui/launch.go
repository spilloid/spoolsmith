package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// version is stamped at release time with -ldflags "-X main.version=$Tag",
// like the CLI's.
var version = ""

func versionString() string {
	if strings.TrimSpace(version) == "" {
		return "(development build)"
	}
	return version
}

// resumeFlag carries an operation into an elevated relaunch. It is only ever
// a description of what to review: the new instance previews it again and
// asks for the usual confirmation of the plan it shows.
const resumeFlag = "--review="

// launchRequest is what the command line asks the desktop to open.
type launchRequest struct {
	// Files are printer files (.ssb) or sets (.zip), from a double-click in
	// Explorer, "Open with", or a drop onto the exe.
	Files []string
	// Review is an operation handed over by an elevated relaunch.
	Review *operation
}

// parseLaunchArgs reads the arguments after the program name. Unknown
// switches are ignored rather than fatal: Explorer and shortcuts can pass
// things the app has no use for, and failing to start helps nobody.
func parseLaunchArgs(args []string) (launchRequest, error) {
	var request launchRequest
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, resumeFlag):
			op, err := decodeReview(strings.TrimPrefix(arg, resumeFlag))
			if err != nil {
				return launchRequest{}, err
			}
			request.Review = &op
		case strings.HasPrefix(arg, "-"):
			continue
		case strings.TrimSpace(arg) != "":
			path := arg
			if absolute, err := filepath.Abs(arg); err == nil {
				path = absolute
			}
			request.Files = append(request.Files, path)
		}
	}
	return request, nil
}

// encodeReview packs an operation into one command-line-safe argument.
func encodeReview(op operation) (string, error) {
	data, err := json.Marshal(op.normalized())
	if err != nil {
		return "", err
	}
	return resumeFlag + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeReview(value string) (operation, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return operation{}, fmt.Errorf("review hand-over is not readable: %w", err)
	}
	var op operation
	if err := json.Unmarshal(data, &op); err != nil {
		return operation{}, fmt.Errorf("review hand-over is not readable: %w", err)
	}
	op = op.normalized()
	if err := op.Validate(); err != nil {
		return operation{}, err
	}
	return op, nil
}

// quoteArg quotes one argument for a Windows command line the way
// CommandLineToArgvW reads it back.
func quoteArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat("\\", slashes*2+1))
			b.WriteRune('"')
			slashes = 0
			continue
		default:
			b.WriteString(strings.Repeat("\\", slashes))
			slashes = 0
			b.WriteRune(r)
			continue
		}
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}

// --- File association -------------------------------------------------------

const (
	printerFileExt    = ".ssb"
	printerFileProgID = "SpoolSmith.PrinterFile"
)

// associationCommand is the shell\open\command value for .ssb files.
func associationCommand(exe string) string {
	return `"` + exe + `" "%1"`
}

// associationTarget extracts the exe path from a registered open command, so
// a portable copy that moved can tell its registration is stale.
func associationTarget(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
		return ""
	}
	if fields := strings.Fields(command); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// --- Clipboard files --------------------------------------------------------

// clipboardFolderName is where one Copy puts its printer files, so the files
// on the clipboard stay put until they are pasted.
func clipboardFolderName(at time.Time) string {
	return "copy-" + at.UTC().Format("20060102-150405.000000000")
}

// staleClipboardFolder reports a folder left by an earlier Copy that is safe
// to clean up: made by us, and old enough that nobody is still pasting it.
func staleClipboardFolder(name string, modified, now time.Time) bool {
	return strings.HasPrefix(name, "copy-") && now.Sub(modified) > 24*time.Hour
}

// clipboardRoot is the per-user folder that holds copied printer files.
func clipboardRoot() string {
	return filepath.Join(os.TempDir(), "SpoolSmith", "clipboard")
}

// printerFileCandidates keeps the paths worth trying to open as printer files.
// The content decides in the end (a set is recognized by what is inside the
// zip), so this only drops folders and obviously unrelated extensions.
func printerFileCandidates(paths []string) (candidates []string, rejected []string) {
	for _, path := range paths {
		info, err := os.Stat(path)
		ext := strings.ToLower(filepath.Ext(path))
		if err != nil || info.IsDir() || (ext != printerFileExt && ext != ".zip") {
			rejected = append(rejected, filepath.Base(path))
			continue
		}
		candidates = append(candidates, path)
	}
	return candidates, rejected
}
