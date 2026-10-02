package install

import (
	"net/url"
	"strings"
)

// Modern Windows exposes IPP through WSDMON. The registry identifies the
// transport, but the queue's Bidi schema provides its actual IPP device URL.
// Keep this reader shared by mutation verification and local status.
const ippConnectionFunctions = `
function Get-SpoolSmithIPPConnection($Queue, $Port) {
 if ($Port.PortMonitor -in @('Internet Port','IPP Port Monitor')) {
  $endpoint = [string]$Port.PrinterHostAddress
  if ([string]::IsNullOrWhiteSpace($endpoint)) { $endpoint = [string]$Port.Name }
  return [PSCustomObject]@{Verified=$true;Endpoint=$endpoint}
 }
 if ($Port.PortMonitor -ne 'WSD Port Monitor') { return [PSCustomObject]@{Verified=$false;Endpoint=''} }
 $record = Get-ItemProperty -LiteralPath ('HKLM:\SYSTEM\CurrentControlSet\Control\Print\Monitors\WSD Port\Ports\' + $Port.Name) -ErrorAction Stop
 if ($record.'Install Protocol' -ne 1 -or $record.'IPP PortId' -notmatch '^IPP-[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$') {
  return [PSCustomObject]@{Verified=$false;Endpoint=''}
 }
 if (-not ('SpoolSmithIPPConnection' -as [type])) {
  Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
[ComImport, Guid("D580DC0E-DE39-4649-BAA8-BF0B85A03A97"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface SpoolSmithBidiSpl {
 void BindDevice([MarshalAs(UnmanagedType.LPWStr)] string name, uint access);
 void UnbindDevice();
 void SendRecv([MarshalAs(UnmanagedType.LPWStr)] string action, SpoolSmithBidiRequest request);
 void MultiSendRecv([MarshalAs(UnmanagedType.LPWStr)] string action, IntPtr requests);
}
[ComImport, Guid("8F348BD7-4B47-4755-8A9D-0F422DF3DC89"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface SpoolSmithBidiRequest {
 void SetSchema([MarshalAs(UnmanagedType.LPWStr)] string schema);
 void SetInputData(uint type, IntPtr data, uint size);
 void GetResult(out int result);
 void GetOutputData(uint index, out IntPtr schema, out uint type, out IntPtr data, out uint size);
 void GetEnumCount(out uint count);
}
public static class SpoolSmithIPPConnection {
 public static string Read(string name) {
  object spool = null, request = null; bool bound = false;
  try {
   spool = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("2A614240-A4C5-4C33-BD87-1BC709331639")));
   request = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("B9162A23-45F9-47CC-80F5-FE0FE9B9E1A2")));
   var s = (SpoolSmithBidiSpl)spool; var r = (SpoolSmithBidiRequest)request;
   const string key = @"\Printer.DeviceInfo.NetworkingInfo:IppDeviceUrl";
   s.BindDevice(name, 2); bound = true; r.SetSchema(key); s.SendRecv("Get", r);
   int result; r.GetResult(out result); if (result != 0) Marshal.ThrowExceptionForHR(result < 0 ? result : unchecked((int)(0x80070000u | (uint)result)));
   uint count; r.GetEnumCount(out count); if (count != 1) throw new InvalidOperationException("IPP URL query did not return one result");
   IntPtr schema = IntPtr.Zero, data = IntPtr.Zero; uint type, size;
   try {
    r.GetOutputData(0, out schema, out type, out data, out size);
    if (!String.Equals(Marshal.PtrToStringUni(schema), key, StringComparison.OrdinalIgnoreCase) || (type != 4 && type != 5) || data == IntPtr.Zero || size < 2 || size > 16384 || size % 2 != 0) throw new InvalidOperationException("IPP URL query returned invalid data");
    return Marshal.PtrToStringUni(data, (int)size / 2).TrimEnd('\0');
   } finally { if (schema != IntPtr.Zero) Marshal.FreeCoTaskMem(schema); if (data != IntPtr.Zero) Marshal.FreeCoTaskMem(data); }
  } finally {
   if (bound) { try { ((SpoolSmithBidiSpl)spool).UnbindDevice(); } catch {} }
   if (request != null) Marshal.FinalReleaseComObject(request);
   if (spool != null) Marshal.FinalReleaseComObject(spool);
  }
 }
}
'@ -ErrorAction Stop
 }
 $endpoint = [SpoolSmithIPPConnection]::Read([string]$Queue.Name)
 return [PSCustomObject]@{Verified=$true;Endpoint=$endpoint}
}
function Get-SpoolSmithIPPKey([string]$Value) {
 $u = [Uri]$Value
 if (-not $u.IsAbsoluteUri -or $u.UserInfo -ne '' -or $u.Query -ne '' -or $u.Fragment -ne '') { throw 'Invalid IPP endpoint' }
 $scheme = $u.Scheme.ToLowerInvariant(); $port = $u.Port
 if ($scheme -notin @('ipp','ipps','http','https')) { throw 'Invalid IPP scheme' }
 if ($port -lt 0) { $port = 631 }
 if ($scheme -eq 'http') { $scheme = 'ipp' }
 if ($scheme -eq 'https') { $scheme = 'ipps' }
 return $scheme + '|' + $u.Host.ToLowerInvariant() + '|' + $port + '|' + $u.AbsolutePath
}
`

func ippEndpointKey(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	scheme, port := strings.ToLower(u.Scheme), u.Port()
	if port == "" {
		switch scheme {
		case "ipp", "ipps":
			port = "631"
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return ""
		}
	}
	switch scheme {
	case "http":
		scheme = "ipp"
	case "https":
		scheme = "ipps"
	case "ipp", "ipps":
	default:
		return ""
	}
	return scheme + "|" + strings.ToLower(u.Hostname()) + "|" + port + "|" + u.EscapedPath()
}

func sameIPPEndpoint(a, b string) bool {
	key := ippEndpointKey(a)
	return key != "" && key == ippEndpointKey(b)
}
