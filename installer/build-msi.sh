#!/usr/bin/env bash
# Builds SpoolSmith-<tag>-x64.msi from a release payload with wixl (GNOME
# msitools), then checks it with installer/check-msi.sh.
#
#   installer/build-msi.sh <tag> <payload-dir> <out-dir>
#
# <payload-dir> must hold exactly the files published in the release ZIP --
# spoolsmith.exe, spoolsmith-gui.exe, README.md, LICENSE -- already signed.
# This script never builds or signs application binaries; it only packages
# what it is given, so the MSI carries the same bytes as the ZIP.
set -euo pipefail

tag="${1:?usage: build-msi.sh <tag> <payload-dir> <out-dir>}"
payload="${2:?usage: build-msi.sh <tag> <payload-dir> <out-dir>}"
out="${3:?usage: build-msi.sh <tag> <payload-dir> <out-dir>}"
here="$(cd "$(dirname "$0")" && pwd)"

version="$("$here/msi-version.sh" "$tag")"

expected="LICENSE README.md spoolsmith-gui.exe spoolsmith.exe"
actual="$(cd "$payload" && find . -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')"
if [[ "$actual" != "$expected" ]]; then
  echo "payload must hold exactly: $expected" >&2
  echo "payload holds:             $actual" >&2
  exit 1
fi

mkdir -p "$out"
msi="$out/SpoolSmith-$tag-x64.msi"
rm -f "$msi"
wixl --arch x64 \
  --define "Version=$version" \
  --define "SourceDir=$(cd "$payload" && pwd)" \
  --output "$msi" \
  "$here/spoolsmith.wxs"

"$here/check-msi.sh" "$msi" "$tag" "$payload"
echo "$msi"
