package install

import (
	"strings"
	"time"
)

// nativeDriverMarker prefixes the one line the native-driver step prints so the
// outcome can be read back without treating the step's other output as data.
const nativeDriverMarker = "SPOOLSMITH-NATIVE-DRIVER:"

// nativeDriverTimeout bounds the whole native-driver step, including a Windows
// Update search, download and install.
var nativeDriverTimeout = 5 * time.Minute

// nativeDriverAttention prefixes an outcome that is not a quiet skip: the step
// changed the queue's driver and could not put the class driver back.
const nativeDriverAttention = "attention"

// nativeDriverFunctions chooses a registered driver for an IPP model. The rule
// is deliberately narrow: the driver name must equal the model name the printer
// reported once case, spaces and punctuation are ignored, and exactly one
// driver may do so. A loosely related OEM driver is never chosen on a guess.
// Normalization keeps ASCII letters and digits only, exactly like
// normalizeDriverName, so the Go status check and this step always agree.
const nativeDriverFunctions = `
function Get-SpoolSmithNorm([string]$s) { return ([regex]::Replace($s, '[^A-Za-z0-9]', '')).ToLowerInvariant() }
function Get-SpoolSmithDriverIsModel([string]$Driver, [string]$Model) {
 $m = Get-SpoolSmithNorm $Model
 return ($m -ne '' -and (Get-SpoolSmithNorm $Driver) -eq $m)
}
function Get-SpoolSmithNativeDriverName([string]$Model, [string[]]$Names) {
 if ((Get-SpoolSmithNorm $Model) -eq '') { return $null }
 $hits = @($Names | Where-Object { $_ -ne 'Microsoft IPP Class Driver' -and (Get-SpoolSmithDriverIsModel $_ $Model) })
 if ($hits.Count -eq 1) { return $hits[0] }
 return $null
}
`

// nativeDriverCommand upgrades a Windows-created IPP queue from the Microsoft
// IPP Class Driver to the printer's own driver when Windows can supply one.
//
// It only asks Windows: drivers already registered, Windows' own driver sources
// by model name (Add-PrinterDriver, which registers a driver Windows already
// has), then a Windows Update driver search whose single exact model match
// Windows itself downloads and installs. SpoolSmith never fetches anything. The
// step never creates, removes or repoints queues or ports and never removes a
// driver. The queue keeps its port; before and after the switch the IPP
// endpoint is verified against the reviewed URL, and the class driver is
// restored if the endpoint cannot be verified afterwards. Every outcome is one
// marker line and the command exits successfully: the queue created by the
// previous command is already a working printer. The one exception is an
// outcome that begins with "attention" (a failed restore), which Install
// reports as an error.
func nativeDriverCommand(plan Plan) string {
	return nativeDriverScript(plan) + "; exit 0"
}

// nativeDriverScript is nativeDriverCommand without the final exit, so tests can
// inspect state afterwards.
func nativeDriverScript(plan Plan) string {
	queue, model, endpoint := powerShellString(plan.PrinterName), powerShellString(plan.NativeDriverModel), powerShellString(plan.IPPURL)
	class := powerShellString(plan.DriverName)
	body := "function Invoke-SpoolSmithNativeDriver { $queue = " + queue + "; $model = " + model + "; $class = " + class + "; $endpoint = " + endpoint + "; $changed = $false; " +
		"function Assert-Endpoint($name, $port, $driver) { " +
		"$q = @(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq $name }); " +
		"if ($q.Count -ne 1 -or $q[0].DriverName -ne $driver -or $q[0].PortName -ne $port) { throw 'queue changed unexpectedly' }; " +
		"$p = @(Get-PrinterPort -ErrorAction Stop | Where-Object { $_.Name -eq $port }); " +
		"if ($p.Count -ne 1) { throw 'port missing' }; " +
		"$c = Get-SpoolSmithIPPConnection $q[0] $p[0]; " +
		"if (-not $c.Verified -or (Get-SpoolSmithIPPKey $c.Endpoint) -cne (Get-SpoolSmithIPPKey $endpoint)) { throw 'IPP endpoint could not be verified' } }; " +
		"try { " +
		"if ((Get-SpoolSmithNorm $model) -eq '') { Write-Output ('" + nativeDriverMarker + " skipped the printer reported no usable model name; keeping ' + $class); return }; " +
		"$printer = @(Get-Printer -ErrorAction Stop | Where-Object { $_.Name -eq $queue }); " +
		"if ($printer.Count -ne 1) { throw 'queue not found' }; " +
		"if ($printer[0].DriverName -ne $class) { Write-Output ('" + nativeDriverMarker + " in-use ' + $printer[0].DriverName); return }; " +
		"$port = $printer[0].PortName; " +
		"$names = { @(Get-PrinterDriver -ErrorAction Stop | Select-Object -ExpandProperty Name) }; " +
		"$name = Get-SpoolSmithNativeDriverName $model (& $names); " +
		"if (-not $name) { try { Add-PrinterDriver -Name $model -ErrorAction Stop } catch {}; $name = Get-SpoolSmithNativeDriverName $model (& $names) }; " +
		"if (-not $name) { " +
		"$session = New-Object -ComObject Microsoft.Update.Session; " +
		"$found = $session.CreateUpdateSearcher().Search(\"IsInstalled=0 and Type='Driver'\"); " +
		"$hits = New-Object -ComObject Microsoft.Update.UpdateColl; " +
		"foreach ($u in $found.Updates) { if ($u.DriverClass -eq 'Printer' -and (Get-SpoolSmithDriverIsModel $u.DriverModel $model)) { [void]$hits.Add($u) } }; " +
		"if ($hits.Count -eq 1) { " +
		"$downloader = $session.CreateUpdateDownloader(); $downloader.Updates = $hits; $d = $downloader.Download(); " +
		"if ($d.ResultCode -eq 2) { $installer = $session.CreateUpdateInstaller(); $installer.Updates = $hits; [void]$installer.Install() }; " +
		"try { Add-PrinterDriver -Name $model -ErrorAction Stop } catch {}; $name = Get-SpoolSmithNativeDriverName $model (& $names) } " +
		"elseif ($hits.Count -gt 1) { Write-Output ('" + nativeDriverMarker + " skipped Windows Update offered more than one match; keeping ' + $class); return } }; " +
		"if (-not $name) { Write-Output ('" + nativeDriverMarker + " skipped Windows has no native driver for ' + $model + '; keeping ' + $class); return }; " +
		// The queue may have been replaced while Windows Update ran.
		"Assert-Endpoint $queue $port $class; " +
		"Set-Printer -Name $queue -DriverName $name -ErrorAction Stop; $changed = $true; " +
		"Assert-Endpoint $queue $port $name; " +
		"Write-Output ('" + nativeDriverMarker + " applied ' + $name) " +
		"} catch { $why = $_.Exception.Message; $verb = 'skipped'; $note = ''; " +
		"if ($changed) { try { Set-Printer -Name $queue -DriverName $class -ErrorAction Stop; $note = '; restored ' + $class } catch { $verb = '" + nativeDriverAttention + "'; $note = '; could not restore ' + $class + ': ' + $_.Exception.Message + '; review the queue' } }; " +
		"Write-Output ('" + nativeDriverMarker + " ' + $verb + ' ' + $why + $note) } }"
	return nativeDriverFunctions + ippConnectionFunctions + body + "; Invoke-SpoolSmithNativeDriver"
}

// NativeDriverOutcome reads the marker line, if any, from the step's output.
func NativeDriverOutcome(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, nativeDriverMarker); ok {
			return strings.TrimSpace(rest)
		}
	}
	return "skipped no result reported; keeping Microsoft IPP Class Driver"
}

// normalizeDriverName keeps ASCII letters and digits, lower-cased. It must
// match Get-SpoolSmithNorm exactly.
func normalizeDriverName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 'a' - 'A')
		}
	}
	return b.String()
}

// sameDriverModel reports whether a driver name is the printer's own model
// name under normalizeDriverName's rule.
func sameDriverModel(driver, model string) bool {
	m := normalizeDriverName(model)
	return m != "" && normalizeDriverName(driver) == m
}
