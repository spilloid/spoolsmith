# Code signing

SpoolSmith's released binaries are Authenticode-signed with
[Azure Artifact Signing](https://learn.microsoft.com/azure/artifact-signing/) (the service
formerly called Trusted Signing). The release pipeline signs
`spoolsmith.exe` and `spoolsmith-gui.exe` after compiling them and before they go into the
release zip, verifies the result, and refuses to publish anything it could not sign.

This supersedes `docs/ci-cd-spec.md`'s original "no code signing" scope note, which deferred
signing as unpriced procurement work.

## Why this matters here

SpoolSmith asks for Administrator rights and mutates the driver store. Two consequences:

- An unsigned binary that does that is exactly the shape of thing SmartScreen and enterprise
  policy are built to stop. Users saw an "unknown publisher" prompt on a tool whose whole pitch
  is careful, reviewable driver installation.
- SpoolSmith's own trust model (`CLAUDE.md`, D-0040) requires an Authenticode check on *vendor*
  driver payloads. Shipping SpoolSmith itself unsigned held it to a lower standard than it holds
  its inputs.

Artifact Signing issues short-lived certificates (three days) from a Microsoft-managed CA, with
no HSM or PFX for anyone to steal. The three-day validity is why every signature must be
RFC 3161 timestamped — see [Timestamping](#timestamping-is-not-optional) below.

## One-time Azure setup

Do this once, in an Azure subscription you control. Portal steps are in Microsoft's
[quickstart](https://learn.microsoft.com/azure/artifact-signing/quickstart); the sequence that
matters:

1. **Register the resource provider** `Microsoft.CodeSigning` on the subscription.
2. **Create an Artifact Signing account.** Note its region — the endpoint must match it exactly
   (see the [endpoint table](https://learn.microsoft.com/azure/artifact-signing/how-to-signing-integrations)).
   For East US that is `https://eus.codesigning.azure.net`.
3. **Complete identity validation.** This is the slow step and it gates everything else: it is a
   real identity check against business (or individual) documentation and takes days, not
   minutes. Start it before you plan a release. Public-trust organization validation requires a
   verifiable business identity; read the current requirements before committing to a date.
4. **Create a certificate profile** of type Public Trust once validation succeeds. Its subject
   is what users will see as the publisher.
5. **Grant the signing role.** Assign **Artifact Signing Certificate Profile Signer** on the
   certificate profile (or the account) to the identity CI will use — step 6. Owner/Contributor
   on the subscription does *not* imply it; signing fails without this specific role.
6. **Create an app registration** for GitHub Actions and add a **federated credential** so no
   secret is ever stored:
   - Entity type: *GitHub Actions deploying Azure resources*
   - Organization `spilloid`, repository `spoolsmith`
   - Entity: *Branch* → `main`, or *Tag*, or — tightest, and what matches this workflow —
     *Environment* if you add a protected `release` environment (see
     [Hardening](#hardening-worth-doing)).

   A federated credential is scoped to that repo and ref. A leaked client secret is not, which
   is why this pipeline uses OIDC instead.

## GitHub repository configuration

Under **Settings → Secrets and variables → Actions**.

Variables (not secret — the account and profile names are visible in the signed binary anyway):

| Variable | Example | Meaning |
| --- | --- | --- |
| `SIGNING_ENDPOINT` | `https://eus.codesigning.azure.net` | Must match the account's region |
| `SIGNING_ACCOUNT` | `spilloid-signing` | Artifact Signing account name |
| `SIGNING_PROFILE` | `spoolsmith-public` | Certificate profile name |
| `SIGNING_EXPECTED_SUBJECT` | `O=Spilloid` | Optional. Substring the signer subject must contain |

Secrets (identifiers rather than credentials, but conventionally kept as secrets):

| Secret | Where it comes from |
| --- | --- |
| `AZURE_CLIENT_ID` | App registration → Application (client) ID |
| `AZURE_TENANT_ID` | App registration → Directory (tenant) ID |
| `AZURE_SUBSCRIPTION_ID` | The subscription holding the signing account |

Set `SIGNING_EXPECTED_SUBJECT` once you know the real subject string. It is the check that
catches a build signed by the wrong certificate profile — a signature that is perfectly valid
and still wrong.

## How the release pipeline uses it

`.github/workflows/release.yml`, in order:

1. **Check release tag and signing configuration** — rejects a malformed tag, a tag that
   disagrees with `VERSION`, and any missing signing configuration. It fails here, before
   building, so a misconfigured repo never reaches the point of quietly publishing unsigned
   binaries.
2. **Build binaries** — `scripts/build-release.ps1 -Stage build`.
3. **Azure login** — OIDC federated credential, no stored secret.
4. **Sign binaries** — `azure/artifact-signing-action@v2` over `dist\*.exe`.
5. **Verify signatures** — `scripts/verify-signature.ps1`; fails the release if anything is
   unsigned, untimestamped, or signed by an unexpected subject.
6. **Package signed release** — `scripts/build-release.ps1 -Stage package`.
7. **Upload release asset** — unchanged.

The build/package split exists because the published SHA-256 must cover the *signed* zip.
Signing after packaging would leave the signature outside the hashed artifact and the hash
describing binaries nobody shipped.

## Signing by hand

`docs/ci-cd-spec.md` requires that a release be reproducible without trusting the pipeline.
`scripts/sign-windows.ps1` is that path. It needs, on Windows:

```powershell
winget install -e --id Microsoft.Azure.ArtifactSigningClientTools
```

That installs the signing dlib, the .NET 8 runtime and the VC++ redistributable. You also need
SignTool from the Windows SDK (10.0.22621 or newer — the version bundled with some toolchains is
too old for the dlib).

```powershell
az login                                    # DefaultAzureCredential picks this up
$env:SPOOLSMITH_SIGN_ENDPOINT = 'https://eus.codesigning.azure.net'
$env:SPOOLSMITH_SIGN_ACCOUNT  = 'spilloid-signing'
$env:SPOOLSMITH_SIGN_PROFILE  = 'spoolsmith-public'

./scripts/build-release.ps1 -Tag v1.0.0 -Sign
```

`-Sign` signs between the build and package stages, exactly as CI does. Without it the script
still produces an unsigned local build, which is the right default for development. Set
`SPOOLSMITH_SIGNTOOL` or `SPOOLSMITH_SIGN_DLIB` if either tool is somewhere the script's search
does not find.

## Verifying a download

Anyone can check a release without installing anything — this deliberately uses
`Get-AuthenticodeSignature` rather than `signtool verify`, because SignTool ships with the
Windows SDK and is **not** present on a stock Windows 11 machine
(`docs/capture-report-review.md` §2.4):

```powershell
# Against the extracted release
./scripts/verify-signature.ps1 -Files spoolsmith/spoolsmith.exe, spoolsmith/spoolsmith-gui.exe

# Or with no repo checkout at all
Get-AuthenticodeSignature .\spoolsmith.exe | Format-List Status, SignerCertificate, TimeStamperCertificate
```

Check the zip against its published sidecar too:

```powershell
(Get-FileHash spoolsmith-v1.0.0-windows-amd64.zip -Algorithm SHA256).Hash.ToLower()
Get-Content spoolsmith-v1.0.0-windows-amd64.zip.sha256
```

## Timestamping is not optional

Artifact Signing certificates are valid for **three days**. The RFC 3161 countersignature from
`http://timestamp.acs.microsoft.com` is what proves the signature was made while the certificate
was live, and it is the only reason a release still verifies a week later.

Both the workflow and `sign-windows.ps1` pass the timestamp URL explicitly, and
`verify-signature.ps1` treats a missing `TimeStamperCertificate` as a hard failure. That check
exists because an untimestamped release looks completely fine on release day and starts failing
on users' machines about three days later — the worst possible failure shape to catch by hand.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `403 Forbidden`, or `SignerSign()` failed | Endpoint region does not match the account's region, or the identity is missing the **Certificate Profile Signer** role |
| `AADSTS700213` / no matching federated credential | The federated credential's repo, ref or entity type does not match the workflow's trigger. A tag-triggered release needs a Tag (or Environment) credential, not a Branch one |
| Certificate profile cannot be created | Identity validation is still pending or was rejected |
| dlib load error from SignTool | SignTool older than 10.0.22621, or an x86/x64 mismatch between SignTool and the dlib |
| Signature valid, publisher wrong | Signed by a different certificate profile — set `SIGNING_EXPECTED_SUBJECT` so CI catches this |

SmartScreen reputation is earned per publisher identity over download volume, not granted on
first signature. Expect some warnings to persist on early v1.x downloads and to fade as the
publisher accrues reputation. Signing is what makes accruing it possible; it is not an
instant switch.

## Hardening worth doing

- **Protected environment.** Add a `release` environment with required reviewers, scope the
  signing secrets to it, add `environment: release` to the release job, and switch the federated
  credential to the Environment entity type. Signing then requires a human approval, and the
  credential cannot be used from any other ref. Left out of the default workflow only because
  referencing an environment that does not exist yet would break the release job.
- **Rotation and offboarding.** There is no private key to rotate. Revoking access means
  removing the role assignment or deleting the federated credential; do that when someone with
  subscription access leaves.
- **Watch the account.** Artifact Signing bills per signing operation above the included
  quota, so a compromised credential shows up as unexpected signing volume. Check current
  [pricing](https://azure.microsoft.com/pricing/details/artifact-signing/) before budgeting.
