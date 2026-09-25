#!/usr/bin/env bash
# Prints the Windows Installer ProductVersion for a SpoolSmith release tag.
#
#   vX.Y.Z  ->  X.Y.Z
#
# Windows Installer compares only the first three fields, and limits them to
# major <= 255, minor <= 255, build <= 65535. A tag outside those limits is an
# error rather than a silently truncated or wrapped version, which would break
# upgrade ordering.
set -euo pipefail

tag="${1:?usage: msi-version.sh vX.Y.Z}"
if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "not a release tag (vX.Y.Z): $tag" >&2
  exit 1
fi
major="${BASH_REMATCH[1]}" minor="${BASH_REMATCH[2]}" patch="${BASH_REMATCH[3]}"
if (( major > 255 || minor > 255 || patch > 65535 )); then
  echo "$tag is outside Windows Installer's version limits (255.255.65535)" >&2
  exit 1
fi
echo "$major.$minor.$patch"
