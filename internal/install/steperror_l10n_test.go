package install

import "testing"

// Windows PowerShell prints an error record's position header in the display
// language ("At line:" only in English), so the reason must not be found by
// that header. Layouts below follow Windows PowerShell 5.1's error view in
// German and French; the message is localized by Windows, the metadata lines
// are not.
func TestStepDiagnosticFindsTheReasonInLocalizedErrorRecords(t *testing.T) {
	const german = "Der Spoolerdienst ist nicht erreichbar.  Stellen Sie sicher, dass der Spoolerdienst ausgeführt wird."
	const french = "Le service spouleur n'est pas accessible. Vérifiez que le service spouleur est en cours d'exécution."
	tail := "+ ... catch { Write-Error $_.Exception.Message; exit 1 }\r\n+           ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\r\n    + CategoryInfo          : NotSpecified: (:) [Write-Error], WriteErrorException\r\n    + FullyQualifiedErrorId : Microsoft.PowerShell.Commands.WriteErrorException\r\n \r\n"
	echo := "[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); $ErrorActionPreference = 'Stop'; try { Get-P\r\nrinter } catch { Write-Error $_.Exception.Message; exit 1 } : "
	cases := map[string]struct{ output, want string }{
		"German":  {echo + "Der Spoolerdienst ist nicht erreichbar.  Stellen Sie sicher, dass der Spoolerdienst ausgefü\r\nhrt wird.\r\nIn Zeile:1 Zeichen:300\r\n" + tail, german},
		"French":  {echo + "Le service spouleur n'est pas accessible. Vérifiez que le service spouleur est en cours d'e\r\nxécution.\r\nAu caractère Ligne:1 : 300\r\n" + tail, french},
		"English": {echo + "The spooler service is not reachable.\r\nAt line:1 char:300\r\n" + tail, "The spooler service is not reachable."},
	}
	for name, c := range cases {
		if got := stepDiagnostic(c.output); got != c.want {
			t.Errorf("%s: stepDiagnostic = %q, want %q", name, got, c.want)
		}
	}
}

// The generated catch block prints the message after reasonMarker on a line of
// its own; that line is used whatever the error view around it looks like.
func TestStepDiagnosticPrefersTheCatchBlocksReasonLine(t *testing.T) {
	const german = "Der Spoolerdienst ist nicht erreichbar."
	output := reasonMarker + german + "\r\n" +
		"[Console]::OutputEncoding = ...; try { Get-Printer } catch { [Console]::Out.WriteLine('SPOOLSMITH' + '-REASON: ' + $_.Exception.Message); Write-Error $_.Exception.Message; exit 1 } : " + german + "\r\n" +
		"In Zeile:1 Zeichen:300\r\n+ ... \r\n    + CategoryInfo          : NotSpecified: (:) [Write-Error], WriteErrorException\r\n    + FullyQualifiedErrorId : Microsoft.PowerShell.Commands.WriteErrorException\r\n"
	if got := stepDiagnostic(output); got != german {
		t.Fatalf("stepDiagnostic = %q, want %q", got, german)
	}
	if got := stepDiagnostic("Created port\r\n    + FullyQualifiedErrorId : Something\r\n"); got != "Created port" {
		t.Fatalf("metadata is never the reason: %q", got)
	}
}
