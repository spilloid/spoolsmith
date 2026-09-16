package intune

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spilloid/spoolsmith/internal/evidence"
	"github.com/spilloid/spoolsmith/internal/install"
)

var testBinary, testBinaryHash string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "spoolsmith-intune-tests-")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "spoolsmith.exe")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.Command(goBinary, "build", "-o", testBinary, "../../cmd/spoolsmith")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build test CLI: %v %s", err, data)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	data, err := os.ReadFile(testBinary)
	if err != nil {
		panic(err)
	}
	testBinaryHash = digest(data)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func testOptions(t *testing.T) Options {
	t.Helper()
	p := install.Profile{Version: 1, Target: "192.0.2.40", PrinterName: "Accounting's $copier “West”", DriverName: "Exact OEM Driver", Evidence: evidence.Evidence{Provenance: "captured", HTTPTitle: "Example Model"}}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := install.SaveProfile(path, p); err != nil {
		t.Fatal(err)
	}
	return Options{ProfilePath: path, BinaryPath: testBinary, BinarySHA256: testBinaryHash, ID: "accounting", Revision: 1, DisplayName: "Accounting copier", Offline: true, DriverPrerequisite: true}
}
func TestExportPinsContentsAndPreservesExistingFiles(t *testing.T) {
	prepared, err := Prepare(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bundle")
	if err = prepared.Export(dest); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Export(dest); err == nil {
		t.Fatal("overwrote existing export")
	}
	if err = verifyFile(filepath.Join(dest, "spoolsmith.exe"), testBinaryHash); err != nil {
		t.Fatal(err)
	}
	if err = verifyFile(filepath.Join(dest, "profile.json"), prepared.Manifest.ProfileSHA256); err != nil {
		t.Fatal(err)
	}
	for _, name := range prepared.Manifest.Files {
		if _, err = os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestInvalidPackagingInputs(t *testing.T) {
	base := testOptions(t)
	for _, change := range []func(*Options){func(o *Options) { o.ID = "../escape" }, func(o *Options) { o.ID = "x'; exit 0" }, func(o *Options) { o.Revision = 0 }, func(o *Options) { o.DisplayName = "" }, func(o *Options) { o.BinarySHA256 = strings.Repeat("0", 64) }, func(o *Options) { o.DriverPrerequisite = false }, func(o *Options) { o.Description = "misleading\ncommand" }} {
		o := base
		change(&o)
		if _, err := Prepare(o); err == nil {
			t.Fatalf("accepted invalid options %+v", o)
		}
	}
}
func TestExportRejectsSourceChangedAfterReview(t *testing.T) {
	o := testOptions(t)
	bytes, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	o.BinaryPath = filepath.Join(t.TempDir(), "cli.exe")
	if err = os.WriteFile(o.BinaryPath, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.BinaryPath, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bundle")
	if err = p.Export(dest); err == nil {
		t.Fatal("exported changed payload")
	}
	if _, err = os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("partial export remained")
	}
}
func TestContentPrepRejectsOutputWithinSource(t *testing.T) {
	source := t.TempDir()
	for _, output := range []string{source, filepath.Join(source, "nested")} {
		if _, err := PrepareContent(context.Background(), "must-not-execute", source, output); err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("%v", err)
		}
	}
}

// shellEnv builds the environment for a generated script run.
//
// PSModulePath is deliberately dropped. On a CI runner whose job shell is
// PowerShell 7, the inherited PSModulePath points at 7's module directories;
// handing that to powershell.exe 5.1 overrides its own $PSHOME\Modules, so
// autoloading stops finding even core cmdlets and the scripts fail with
// "Get-FileHash is not recognized". Removing the variable lets each shell
// compute its own default, which is what a real endpoint has.
func shellEnv(extra ...string) []string {
	base := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(entry), "PSMODULEPATH=") {
			continue
		}
		base = append(base, entry)
	}
	return append(base, extra...)
}

func powershell(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("SPOOLSMITH_PWSH"); path != "" {
		return path
	}
	for _, name := range []string{"powershell.exe", "pwsh"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell unavailable; set SPOOLSMITH_PWSH for generated-script integration tests")
	return ""
}
func psString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func TestGeneratedPowerShellParses(t *testing.T) {
	shell := powershell(t)
	p, err := Prepare(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range p.files {
		if !strings.HasSuffix(name, ".ps1") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			os.WriteFile(path, data, 0600)
			script := `$tokens=$null;$parseErrors=$null;[Management.Automation.Language.Parser]::ParseFile(` + psString(path) + `,[ref]$tokens,[ref]$parseErrors)|Out-Null;if($parseErrors.Count){$parseErrors|Out-String|Write-Output;exit 1}`
			parse := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", script)
			parse.Env = shellEnv()
			if out, err := parse.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v %s", name, err, out)
			}
		})
	}
}
func TestDetectionChecksRealConfigurationAndEmitsOnlyOnMatch(t *testing.T) {
	shell := powershell(t)
	p, err := Prepare(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "detect.ps1")
	os.WriteFile(path, p.files["detect.ps1"], 0600)
	for _, failure := range []string{"", "queue", "driver", "registration", "port", "address", "protocol", "number", "revision", "pending", "spooler", "tampered-profile", "reparse", "untrusted-owner", "writable-state"} {
		t.Run("mismatch-"+failure, func(t *testing.T) {
			m := p.Manifest
			if failure == "revision" {
				m.Revision++
			}
			if failure == "tampered-profile" {
				m.Profile.DriverName = "Other"
			}
			b, _ := json.Marshal(m)
			prelude := `$m=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(` + psString(base64.StdEncoding.EncodeToString(b)) + `))|ConvertFrom-Json;$mode=` + psString(failure) + `;
function Get-Item {param($LiteralPath,[switch]$Force);$attributes=0;if($mode -eq 'reparse'){$attributes=1024};[pscustomobject]@{Attributes=$attributes;Parent=$null}}
function Get-Acl {param($LiteralPath);$acl=[pscustomobject]@{};$acl|Add-Member ScriptMethod GetOwner {param($type);$sid='S-1-5-18';if($mode -eq 'untrusted-owner'){$sid='S-1-5-11'};[pscustomobject]@{Value=$sid}};$acl|Add-Member ScriptMethod GetAccessRules {param($explicit,$inherited,$type);if($mode -eq 'writable-state'){[pscustomobject]@{AccessControlType='Allow';FileSystemRights=2;IdentityReference=[pscustomobject]@{Value='S-1-5-11'}}}};return $acl}
function Get-Content {param($LiteralPath,[switch]$Raw) return ($m|ConvertTo-Json -Depth 20)}
function Test-Path {param($LiteralPath) return ($mode -eq 'pending')}
function Get-Printer { [CmdletBinding()]param();if($mode -eq 'spooler'){throw 'spooler down'};if($mode -eq 'queue'){return};$driver=$m.profile.driver_name;if($mode -eq 'driver'){$driver='Other'};[pscustomobject]@{Name=$m.profile.printer_name;DriverName=$driver;PortName=('SpoolSmith-'+$m.profile.target)} }
function Get-PrinterDriver { [CmdletBinding()]param();if($mode -ne 'registration'){[pscustomobject]@{Name=$m.profile.driver_name}} }
function Get-PrinterPort { [CmdletBinding()]param();if($mode -eq 'port'){return};$address=$m.profile.target;$protocol=1;$number=9100;if($mode -eq 'address'){$address='192.0.2.41'};if($mode -eq 'protocol'){$protocol=2};if($mode -eq 'number'){$number=515};[pscustomobject]@{Name=('SpoolSmith-'+$m.profile.target);PrinterHostAddress=$address;Protocol=$protocol;PortNumber=$number} }
& ` + psString(path)
			cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", prelude)
			cmd.Env = shellEnv()
			out, err := cmd.CombinedOutput()
			if failure == "" {
				if err != nil || !strings.Contains(string(out), "configured locally") {
					t.Fatalf("%v %s", err, out)
				}
			} else if err == nil || len(out) != 0 {
				t.Fatalf("mismatch emitted output or passed: %v %q", err, out)
			}
		})
	}
}

// These tests execute the actual lifecycle scripts with only Windows inventory,
// privilege and ACL boundaries replaced. They are not a Windows or Intune pilot.
func TestGeneratedLifecycleRepeatUpdateRecoveryAndRemoval(t *testing.T) {
	shell := powershell(t)
	root := filepath.Join(t.TempDir(), "machine")
	os.Mkdir(root, 0700)
	o := testOptions(t)
	mock := `
function Assert-Platform {}
function Assert-Protected([string]$Path) {}
function Initialize-Root { return $env:SPOOLSMITH_TEST_ROOT }
function Get-Printer { [CmdletBinding()]param();$path=Join-Path $env:SPOOLSMITH_TEST_ROOT 'queue.json';if(Test-Path -LiteralPath $path){$q=Read-JSON $path;[pscustomobject]@{Name=$q.printer_name;DriverName=$q.driver_name;PortName=('SpoolSmith-'+$q.target)}} }
function Invoke-SpoolSmith([string]$Directory,[string]$Operation,[bool]$Offline,[string]$LogDir) {
 $m=Read-JSON (Join-Path $Directory 'deployment.json');$path=Join-Path $env:SPOOLSMITH_TEST_ROOT 'queue.json'
 $q=$null;if(Test-Path -LiteralPath $path){$q=Read-JSON $path}
 if($Operation -eq 'status'){$match=$q -and $q.printer_name -eq $m.profile.printer_name -and $q.driver_name -eq $m.profile.driver_name -and $q.target -eq $m.profile.target;$code=3;if($match){$code=0};return @{Code=$code;Data=@{compliant=[bool]$match};Error=''}}
 if($Operation -eq 'remove'){if($q){Remove-Item -LiteralPath $path};return @{Code=0;Data=$null;Error=''}}
 if($Operation -eq 'add' -and $q -and ($q.target -ne $m.profile.target -or $q.driver_name -ne $m.profile.driver_name)){return @{Code=1;Data=$null;Error='conflict'}}
 Save-JSON $path $m.profile
 if($env:SPOOLSMITH_TEST_FAIL -eq 'after-mutation'){return @{Code=1;Data=$null;Error='injected failure after mutation'}}
 return @{Code=0;Data=$null;Error=''}
}
`
	export := func(opts Options) string {
		t.Helper()
		p, e := Prepare(opts)
		if e != nil {
			t.Fatal(e)
		}
		dest := filepath.Join(t.TempDir(), "source")
		if e = p.Export(dest); e != nil {
			t.Fatal(e)
		}
		f, e := os.OpenFile(filepath.Join(dest, "runtime.ps1"), os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		f.WriteString(mock)
		f.Close()
		return dest
	}
	execute := func(script string, fail bool) error {
		t.Helper()
		cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-File", script)
		cmd.Env = shellEnv("SPOOLSMITH_TEST_ROOT=" + root)
		if fail {
			cmd.Env = append(cmd.Env, "SPOOLSMITH_TEST_FAIL=after-mutation")
		}
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Logf("script failure: %s", out)
		}
		return e
	}
	first := export(o)
	for i := 0; i < 2; i++ {
		if err := execute(filepath.Join(first, "install.ps1"), false); err != nil {
			t.Fatal(err)
		}
	}
	// A second deployment may not adopt a queue claimed by the first, even
	// with explicit matching-queue adoption enabled.
	other := o
	other.ID = "other"
	other.Adopt = true
	if err := execute(filepath.Join(export(other), "install.ps1"), false); err == nil {
		t.Fatal("adopted another deployment's queue")
	}
	p, err := install.LoadProfile(o.ProfilePath)
	if err != nil {
		t.Fatal(err)
	}
	p.Target = "192.0.2.41"
	nextProfile := filepath.Join(t.TempDir(), "next.json")
	install.SaveProfile(nextProfile, p)
	next := o
	next.ProfilePath = nextProfile
	if err := execute(filepath.Join(export(next), "install.ps1"), false); err == nil {
		t.Fatal("changed config at same revision")
	}
	next.Revision = 2
	second := export(next)
	if err := execute(filepath.Join(second, "install.ps1"), true); err == nil {
		t.Fatal("injected failure was reported successful")
	}
	if _, err := os.Stat(filepath.Join(root, o.ID, "pending.json")); err != nil {
		t.Fatal("interrupted update lost recovery inputs")
	}
	if err := execute(filepath.Join(second, "install.ps1"), false); err != nil {
		t.Fatal("retry failed", err)
	}
	if err := execute(filepath.Join(first, "install.ps1"), false); err == nil {
		t.Fatal("downgrade accepted")
	}
	// The persisted uninstaller works after BOTH source directories disappear.
	os.RemoveAll(first)
	os.RemoveAll(second)
	uninstall := filepath.Join(root, o.ID, "uninstall.ps1")
	for i := 0; i < 2; i++ {
		if err := execute(uninstall, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "queue.json")); !os.IsNotExist(err) {
		t.Fatal("queue remains")
	}
	if _, err := os.Stat(filepath.Join(root, o.ID, "current.json")); !os.IsNotExist(err) {
		t.Fatal("ownership claim remains")
	}
}

func TestRuntimePreservesNativeExitAndJSON(t *testing.T) {
	shell := powershell(t)
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper.go")
	code := `package main
import("fmt";"os";"strconv")
func main(){fmt.Print("{\"compliant\":true}");c,_:=strconv.Atoi(os.Getenv("SPOOLSMITH_TEST_EXIT"));os.Exit(c)}`
	if err := os.WriteFile(helper, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	binary := filepath.Join(dir, "spoolsmith.exe")
	if data, err := exec.Command(goBinary, "build", "-o", binary, helper).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, data)
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	p := []byte(`{}`)
	os.WriteFile(filepath.Join(dir, "profile.json"), p, 0600)
	m := Manifest{BinarySHA256: digest(b), ProfileSHA256: digest(p)}
	data, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(dir, "deployment.json"), data, 0600)
	script, err := templates.ReadFile("templates/runtime.ps1")
	if err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(dir, "runtime.ps1")
	os.WriteFile(runtimePath, script, 0600)
	for _, want := range []int{0, 4} {
		cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", ". "+psString(runtimePath)+"; Invoke-SpoolSmith "+psString(dir)+" 'status' $false "+psString(dir)+" | ConvertTo-Json -Depth 10")
		cmd.Env = shellEnv(fmt.Sprintf("SPOOLSMITH_TEST_EXIT=%d", want))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		var result struct {
			Code *int
			Data struct{ Compliant bool }
		}
		if err = json.Unmarshal(out, &result); err != nil || result.Code == nil || *result.Code != want || !result.Data.Compliant {
			t.Fatalf("want=%d result=%s error=%v", want, out, err)
		}
	}
}
