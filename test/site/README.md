# Product site browser checks

From the repository root, with Node/npm and Python available:

```sh
npm ci --prefix test/site
npx --prefix test/site playwright install chromium
python3 -m http.server 8765 --bind 127.0.0.1 --directory docs
# In another terminal, from the repository root:
node test/site/check.cjs
```

For an already deployed site:

```sh
SITE_URL=https://spilloid.github.io/spoolsmith/ node test/site/check.cjs
```

Checks Chromium at 360, 390, 768 and 1440 CSS pixels, all quickstart tabs, keyboard
tab navigation, copy-to-clipboard, images, local anchors and no-JavaScript guides.
Axe checks WCAG 2 A/AA and 2.1 AA rules; this is an automated audit, not a claim of
complete accessibility conformance. Screenshots go to ignored `dist/web-qc/`, or
`SITE_QC_OUTPUT` if set. These dependencies are test-only; the site has no build step.
