#!/usr/bin/env bash
# Checks a built SpoolSmith MSI against what it must be, reading its tables
# with msiinfo and its files with msiextract (GNOME msitools).
#
#   installer/check-msi.sh <msi> <tag> [<payload-dir>]
#
# With <payload-dir>, every packaged file must be byte-identical to it.
set -euo pipefail

msi="${1:?usage: check-msi.sh <msi> <tag> [payload-dir]}"
msi="$(cd "$(dirname "$msi")" && pwd)/$(basename "$msi")"
tag="${2:?usage: check-msi.sh <msi> <tag> [payload-dir]}"
payload="${3:-}"
[[ -n "$payload" ]] && payload="$(cd "$payload" && pwd)"
here="$(cd "$(dirname "$0")" && pwd)"
version="$("$here/msi-version.sh" "$tag")"

upgrade_code="{62631A50-5FE9-4696-B410-92BEAAE13C52}"
failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }
ok() { echo "ok    $*"; }

# msiinfo export prints a table as IDT: three header lines, then tab-separated rows.
table() { msiinfo export "$msi" "$1" | tail -n +4 | tr -d '\r'; }
property() { table Property | awk -F'\t' -v k="$1" '$1 == k { print $2 }'; }

expect_property() {
  local got
  got="$(property "$1")"
  if [[ "$got" == "$2" ]]; then ok "$1 = $2"; else fail "$1 is '$got', expected '$2'"; fi
}

expect_property ProductName "SpoolSmith"
expect_property ProductVersion "$version"
expect_property Manufacturer "Joseph Spillers"
expect_property UpgradeCode "$upgrade_code"

product_code="$(property ProductCode)"
if [[ "$product_code" =~ ^\{[0-9A-F-]{36}\}$ && "$product_code" != "$upgrade_code" ]]; then
  ok "ProductCode $product_code"
else
  fail "ProductCode '$product_code' is missing or equals the UpgradeCode"
fi

template="$(msiinfo suminfo "$msi" | awk -F': ' '/^Template/ { print $2 }' | tr -d '\r')"
if [[ "$template" == "x64;1033" ]]; then ok "platform x64"; else fail "summary Template is '$template', expected x64;1033"; fi

# Per-machine: ALLUSERS=1 (InstallScope="perMachine").
expect_property ALLUSERS "1"

# Installed under Program Files\SpoolSmith.
dirs="$(table Directory)"
if grep -qP '^INSTALLDIR\tProgramFiles64Folder\tSpoolSmith$' <<<"$dirs"; then
  ok "INSTALLDIR is ProgramFiles64Folder\\SpoolSmith"
else
  fail "INSTALLDIR is not ProgramFiles64Folder\\SpoolSmith:"$'\n'"$dirs"
fi

# Exactly the four release files, all in INSTALLDIR.
files="$(table File | awk -F'\t' '{ n = $3; sub(/^[^|]*\|/, "", n); print n }' | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')"
if [[ "$files" == "LICENSE README.md spoolsmith-gui.exe spoolsmith.exe" ]]; then
  ok "files: $files"
else
  fail "packaged files are '$files'"
fi
components="$(table Component | awk -F'\t' '{ print $3 }' | LC_ALL=C sort -u | tr '\n' ' ' | sed 's/ $//')"
if [[ "$components" == "INSTALLDIR" ]]; then ok "every component installs to INSTALLDIR"; else fail "components install to: $components"; fi

# One Start Menu shortcut, "SpoolSmith", to the GUI.
shortcuts="$(table Shortcut)"
if [[ "$(wc -l <<<"$shortcuts")" -eq 1 ]] &&
   awk -F'\t' '$2 == "ProgramMenuFolder" && $3 ~ /(^|\|)SpoolSmith$/ && $5 == "[#SpoolSmithGuiExe]" { found = 1 } END { exit !found }' <<<"$shortcuts"; then
  ok "Start Menu shortcut SpoolSmith -> spoolsmith-gui.exe"
else
  fail "unexpected Shortcut table:"$'\n'"$shortcuts"
fi

# Major upgrade: older versions are removed, newer ones block a downgrade.
upgrades="$(table Upgrade)"
if grep -qF "$upgrade_code" <<<"$upgrades"; then ok "Upgrade table keyed by the UpgradeCode"; else fail "no Upgrade row for $upgrade_code"; fi
# The row that removes earlier installs must include this very version
# (msidbUpgradeAttributesVersionMaxInclusive, 0x200), so a rebuild of the same
# version replaces the old one instead of installing beside it.
# awk keeps empty columns (VersionMin is empty on this row); msidbUpgrade-
# AttributesVersionMaxInclusive is bit 0x200 (512) of Attributes.
if awk -F'\t' -v code="$upgrade_code" -v v="$version" \
     '$1 == code && $3 == v && int($5 / 512) % 2 == 1 { found = 1 } END { exit !found }' <<<"$upgrades"; then
  ok "same-version rebuilds replace, not duplicate"
else
  fail "no Upgrade row that includes $version itself:"$'\n'"$upgrades"
fi

# Nothing beyond what this installer is meant to do.
for forbidden in ServiceInstall ServiceControl Environment Extension ProgId Verb Class TypeLib; do
  if [[ -n "$(table "$forbidden" 2>/dev/null)" ]]; then fail "unexpected $forbidden rows"; fi
done
if [[ -n "$(table Registry 2>/dev/null)" ]]; then fail "unexpected Registry rows (the .ssb association stays per-user, in the app)"; fi
ok "no services, environment, file associations or registry writes"

if [[ -n "$payload" ]]; then
  extracted="$(mktemp -d)"
  trap 'rm -rf "$extracted"' EXIT
  (cd "$extracted" && msiextract "$msi" >/dev/null)
  for name in spoolsmith.exe spoolsmith-gui.exe README.md LICENSE; do
    packaged="$(find "$extracted" -type f -name "$name" | head -n 1)"
    if [[ -n "$packaged" ]] && cmp -s "$packaged" "$payload/$name"; then
      ok "$name is byte-identical to the release payload"
    else
      fail "$name in the MSI differs from the release payload"
    fi
  done
fi

if (( failures > 0 )); then
  echo "$failures check(s) failed for $msi" >&2
  exit 1
fi
echo "All checks passed for $msi"
