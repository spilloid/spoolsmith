package install

// Listing a driver name is not enough: Windows can retain its registration
// after the configuration DLL or another required file has gone missing. Such
// a registration must not suppress installing a carried driver. These checks
// only read inventory and files; preview must never load driver UI or trigger
// Windows' interactive driver installation.
const driverHealthFunctions = `
function Get-SpoolSmithUsableDriver([string]$Name) {
 $arch = $env:PROCESSOR_ARCHITEW6432;
 if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE };
 $environment = switch ($arch) {
  'AMD64' { 'Windows x64' }
  'ARM64' { 'Windows ARM64' }
  'x86' { 'Windows NT x86' }
  default { throw 'Cannot determine the native Windows printer driver environment' }
 };
 $drivers = @(Get-PrinterDriver -ErrorAction Stop | Where-Object { $_.Name -eq $Name -and $_.PrinterEnvironment -eq $environment });
 if ($drivers.Count -ne 1) { return $null };
 $driver = $drivers[0];
 if ($driver.MajorVersion -notin @(3, 4)) { return $null };
 if ([string]::IsNullOrWhiteSpace($driver.Path) -or -not [IO.Path]::IsPathRooted($driver.Path)) { return $null };
 if ([string]::IsNullOrWhiteSpace($driver.DataFile)) { return $null };
 if ($driver.MajorVersion -eq 3 -and [string]::IsNullOrWhiteSpace($driver.ConfigFile)) { return $null };
 $directory = Split-Path -Parent $driver.Path;
 $files = @($driver.Path, $driver.DataFile);
 if (-not [string]::IsNullOrWhiteSpace($driver.ConfigFile)) { $files += $driver.ConfigFile };
 $files += @($driver.DependentFiles);
 foreach ($file in $files) {
  if ([string]::IsNullOrWhiteSpace($file)) { continue };
  $path = [string]$file;
  if (-not [IO.Path]::IsPathRooted($path)) { $path = Join-Path $directory $path };
  if (-not (Test-Path -LiteralPath $path -PathType Leaf -ErrorAction Stop)) { return $null }
 };
 return $driver
}
`
