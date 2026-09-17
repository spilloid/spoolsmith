# Post-release quality review — September 17, 2026

Scope: desktop UX, saved-file safety, documentation coherence and the product site
following v0.6.0. Intune entrypoints remain disabled. Application fixes here are on
main after the v0.6.0 tag; the existing release ZIP has not been replaced.

## Findings and changes

- An invalid or newer-schema profile could still open in the editor after strict
  decoding failed. The selected file now has Edit disabled, and the editor checks
  again before opening. A native GUI regression checks disabled actions and that
  the invalid file is preserved.
- Exporting a JSON collection into the profile folder polluted the next export:
  it tried to read that collection as an individual profile. Export now rejects
  the same folder, including directory aliases, with an actionable error. Tests
  verify no file was created and the library remains exportable.
- Collection validation now checks the formatted profile size that import writes,
  including its newline, before importing any files.
- Empty libraries explain how to import, save or open another folder. A populated
  folder switch selects its first setup consistently.
- The product site leads with the released ZIP, a desktop copy workflow, file-type
  explanations, support boundaries, reference links and FAQ. Quickstart tabs wrap
  on narrow screens and all guides are visible with JavaScript disabled.
- Removed source-build instructions from the end-user download path. Corrected
  the single-PC driver-staging claim, outdated live-removal status and held Intune
  instructions that previously implied rebuilding would enable the feature.

## Verification

- Local Go suite and vet: passed. Windows x64 cross-build and vet: passed.
- Chromium responsive checks at 360, 390, 768 and 1440 CSS pixels: passed.
- All quickstart tabs, keyboard navigation, clipboard, images, local anchors and
  no-JavaScript guides: passed. No page JavaScript errors.
- Axe automated WCAG 2 A/AA and 2.1 AA checks: zero violations at all four widths.
  This does not replace manual assistive-technology testing.
- Browser check source and reproduction instructions: [`test/site`](../../test/site).
- Native Windows desktop, hosted CI and live Pages checks: results recorded below
  after the reviewed changes are pushed.

No new physical printer, two-PC, live address-change, high-DPI or Intune tenant
validation was performed. The [roadmap](../roadmap.md) separates those outstanding
checks from known limitations and future feature scope.
