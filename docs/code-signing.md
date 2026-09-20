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

## Setup

Two things cannot be scripted and gate everything else. Do them in the Azure portal, once:

1. **Register the resource provider** `Microsoft.CodeSigning` on the subscription, and
   **create an Artifact Signing account**. Note its region: the endpoint must match it exactly
   (see the [endpoint table](https://learn.microsoft.com/azure/artifact-signing/how-to-signing-integrations)).
   East US is `https://eus.codesigning.azure.net`.
2. **Complete identity validation and create a Public Trust certificate profile.** Validation
   is a real document check and takes days, not minutes, so start it before you plan a
   release. The profile's subject is what users see as the publisher. Microsoft's
   [quickstart](https://learn.microsoft.com/azure/artifact-signing/quickstart) walks through
   both.

Everything after that is one idempotent script:

```powershell
az login
./scripts/setup-signing.ps1 -AccountName jdspille -ResourceGroup RG0 -ProfileName primary-profile
```

It needs `az login` as someone who can create app registrations and assign roles, and GitHub
access to the repository as an admin (`gh auth login`, or a `GH_TOKEN` with `repo` scope). It
refuses to continue unless the certificate profile is `Active`, checks before it creates each
thing, and changes nothing on a second run. It:

1. Creates an **app registration and service principal**, `spoolsmith-release-signing`.
2. Adds a **federated credential** so no secret is ever stored. It trusts exactly one subject,
   `<GitHub's subject prefix>:environment:release`. See [The OIDC subject](#the-oidc-subject).
3. Grants **Artifact Signing Certificate Profile Signer** on the certificate profile only, not
   the account and not the subscription. Owner or Contributor does *not* imply this role;
   signing fails without it.
4. Creates the `release` **GitHub environment** and sets its variables and secrets.

### The OIDC subject

The release job declares `environment: release`, so the subject GitHub presents to Azure is
fixed by the environment whatever triggered the run. That matters: a published release runs as
the new tag, while a manual `workflow_dispatch` runs as `main`, so a Branch credential misses
the first and a Tag credential misses the second (and would need a new entry every release).

This repository has GitHub's **immutable subject claims** on, so the subject embeds numeric
IDs, not names:

```
repo:spilloid@19334728/spoolsmith@1356635288:environment:release
```

That is the safer form, since a renamed, deleted or re-created repository cannot inherit the
trust, and it is why the script asks GitHub for the prefix
(`gh api repos/<repo>/actions/oidc/customization/sub`) instead of assuming
`repo:spilloid/spoolsmith`. A credential written with the name form fails with `AADSTS700213`.

## GitHub configuration

Everything is scoped to the **`release` environment** (Settings → Environments → release), so
only a job that declares that environment can read it. `setup-signing.ps1` sets all of it.

| Variable | Value here | Meaning |
| --- | --- | --- |
| `SIGNING_ENDPOINT` | `https://eus.codesigning.azure.net` | Must match the account's region |
| `SIGNING_ACCOUNT` | `jdspille` | Artifact Signing account name |
| `SIGNING_PROFILE` | `primary-profile` | Certificate profile name |
| `SIGNING_EXPECTED_SUBJECT` | `CN=Joseph Spillers` | Substring the signer subject must contain |

| Secret | Where it comes from |
| --- | --- |
| `AZURE_CLIENT_ID` | The app registration's application (client) ID |
| `AZURE_TENANT_ID` | The directory (tenant) ID |
| `AZURE_SUBSCRIPTION_ID` | The subscription holding the signing account |

`SIGNING_EXPECTED_SUBJECT` catches a build signed by the wrong certificate profile, which is a
signature that is perfectly valid and still wrong. The script fills it from the profile's
common name.

**Deployment branches.** The environment only accepts jobs running on `main` or a `v*` tag.
Without that, any branch anyone with write access can push could run in the environment and
sign as this identity; a throwaway branch did exactly that during setup, then was refused once
the restriction was on ("Branch is not allowed to deploy to release due to environment
protection rules"). A published release runs as its `vX.Y.Z` tag and a manual run as `main`,
so both still work.

`setup-signing.ps1` applies this itself (`-DeploymentBranches`, `-DeploymentTags`) and removes
any other rule, so the environment ends up allowing exactly what was asked for. The one time
you want it off is proving a workflow from a throwaway branch before merging it: pass
`-SkipDeploymentPolicy`, run the check, then re-run without it.

## Using this for another repository

The script is not specific to SpoolSmith. Run it once per repository that ships Windows
executables:

```powershell
./scripts/setup-signing.ps1 -AccountName jdspille -ResourceGroup RG0 -ProfileName primary-profile `
    -Repo Spillers-Technology/netviz -EnableImmutableSubject
```

Each repository gets its **own** app registration (`<repo>-release-signing`), federated
credential, role assignment and environment, and shares only the certificate profile. That
costs a few more Azure objects and buys three things: access is revoked per repository, one
repository's credential cannot sign for another, and a credential's subject names exactly one
repository. `-EnableImmutableSubject` turns on GitHub's numeric-ID subject claims first, which
is what this repository already has; the script reads whichever form GitHub is using.

Then, in that repository: copy `.github/actions/sign-release` and `scripts/verify-signature.ps1`,
call the action from the release job between building and packaging, and add a signing-check
workflow. Only repositories that actually ship a Windows executable need any of this; a
container image, a website or an Android package has nothing for Authenticode to sign.

The certificate profile is one identity, `CN=Joseph Spillers`. Every repository signed this way
publishes as that person, whoever owns the repository. A company publisher name needs
organization validation and a second certificate profile, which is a separate decision.

## How the release pipeline uses it

`.github/workflows/release.yml`, in order:

1. **Check release tag and signing configuration** — rejects a malformed tag, a tag that
   disagrees with `VERSION`, and any missing signing configuration. It fails here, before
   building, so a misconfigured repo never reaches the point of quietly publishing unsigned
   binaries.
2. **Build binaries** — `scripts/build-release.ps1 -Stage build`.
3. **Sign and verify binaries** — the `.github/actions/sign-release` composite action: Azure
   login over OIDC, `azure/artifact-signing-action@v2` over `dist\*.exe`, then
   `scripts/verify-signature.ps1`, which fails the release if anything is unsigned,
   untimestamped, or signed by an unexpected subject.
4. **Package signed release** — `scripts/build-release.ps1 -Stage package`.
5. **Upload release asset** — unchanged.

The build/package split exists because the published SHA-256 must cover the *signed* zip.
Signing after packaging would leave the signature outside the hashed artifact and the hash
describing binaries nobody shipped.

The signing action authenticates through `DefaultAzureCredential`. `azure/login` leaves an
**Azure CLI session**; it does not export `AZURE_*` variables or a token file. So the CLI
credential is the only one that can work, and the action enables it and excludes the rest.
Leaving only the environment and workload-identity credentials on fails after a successful
login with "EnvironmentCredential authentication unavailable".

## Checking signing without releasing

`.github/workflows/signing-check.yml` builds the real binaries, signs them through the same
composite action a release uses, verifies them, and keeps them as a one-day workflow artifact.
It publishes nothing. Run it after changing signing configuration and before tagging:

```sh
gh workflow run signing-check.yml --ref main
gh run download <run-id> -n signed-binaries      # then run scripts/verify-signature.ps1 on them
```

GitHub only dispatches a workflow that exists on the default branch, so it becomes runnable
once this lands on `main`. It has to run from `main` or a `v*` tag, per the deployment
restriction above.

## Setup record

What was actually done to bring signing up for `spilloid/spoolsmith`, on 2026-09-19, and what
went wrong on the way. The account, profile and identity validation already existed.

**Found.** Subscription `Azure subscription 1`, provider `Microsoft.CodeSigning` registered,
signing account `jdspille` (Basic, East US, `RG0`), certificate profile `primary-profile`
(Public Trust, Active) issuing certificates for
`CN=Joseph Spillers, O=Joseph Spillers, L=Indianapolis, S=in, C=US`. No app registration
existed yet.

**Done, by `scripts/setup-signing.ps1`.** App registration and service principal
`spoolsmith-release-signing`; a federated credential; the Signer role on `primary-profile`
only; the `release` environment with four variables and three secrets. Then the deployment
restriction to `main` and `v*` tags, applied by hand with the `gh api` calls above.

**How it was run.** `az` and `gh` were installed with `winget`. Azure access came from an
interactive `az login --use-device-code`. GitHub access reused the OAuth token Git Credential
Manager already held (`git credential fill`), passed to `gh` through `GH_TOKEN` for that one
process and never written down.

**Faults found by a dry run, before any release.** A read-through could not have found either:

1. *`AADSTS700213`, no matching federated identity record.* The credential trusted
   `repo:spilloid/spoolsmith:environment:release`, but GitHub presented the ID-based subject
   (see [The OIDC subject](#the-oidc-subject)). The script now reads the prefix from GitHub and
   updates a credential that trusts the wrong subject.
2. *`EnvironmentCredential authentication unavailable`, after a successful login.* The signing
   action had the wrong `DefaultAzureCredential` sources enabled (see
   [How the release pipeline uses it](#how-the-release-pipeline-uses-it)).

**Proved.** After both fixes the signing check signed `spoolsmith.exe` and `spoolsmith-gui.exe`.
The artifact was downloaded and checked on a separate Windows machine: both report `Valid`,
issued by `CN=Microsoft ID Verified CS EOC CA 04`, signed by `CN=Joseph Spillers`, with a
countersignature from `Microsoft Public RSA Time Stamping Authority`. The certificate itself
lives three days, which is why the timestamp is what keeps a release verifiable.

**First signed release, v1.0.0.** Published the same day, first as a *prerelease* so GitHub did
not mark it Latest while it was unchecked. The release workflow ran from the tag and uploaded
`spoolsmith-v1.0.0-windows-amd64.zip` and its `.sha256`. The published assets were then
downloaded from the public URL, unauthenticated, and checked as a user would:

- the zip's SHA-256 matched the published sidecar;
- both executables reported `Valid`, signed by `CN=Joseph Spillers`, with the Microsoft
  time-stamp countersignature;
- the product icon was present at 16, 32, 48 and 256px, and the CLI carried the `v1.0.0` stamp.

Only then was the release promoted to Latest. Doing it in that order is worth keeping for the
next release: a failure between publish and verify leaves a prerelease to fix and re-run with
`workflow_dispatch`, not a broken "Latest" that `releases/latest` sends everyone to.

The CLI has no `--version` flag; the stamp is visible only in bundle metadata. That is a gap,
not part of this change.

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
$env:SPOOLSMITH_SIGN_ACCOUNT  = 'jdspille'
$env:SPOOLSMITH_SIGN_PROFILE  = 'primary-profile'

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
| `AADSTS700213` / no matching federated credential | The credential's subject does not match the one GitHub presents. The failing log prints it as `subject claim`; compare it with [The OIDC subject](#the-oidc-subject). Re-running `scripts/setup-signing.ps1` corrects it. The name form, Branch and Tag credentials do not match this job |
| `EnvironmentCredential authentication unavailable` after a successful login | The signing action has the wrong credential source enabled; it needs the Azure CLI credential |
| Job fails instantly with "not allowed to deploy to release" | The run is on a branch or tag the environment's deployment restriction does not allow (`main` and `v*` only) |
| Certificate profile cannot be created | Identity validation is still pending or was rejected |
| dlib load error from SignTool | SignTool older than 10.0.22621, or an x86/x64 mismatch between SignTool and the dlib |
| Signature valid, publisher wrong | Signed by a different certificate profile — set `SIGNING_EXPECTED_SUBJECT` so CI catches this |

SmartScreen reputation is earned per publisher identity over download volume, not granted on
first signature. Expect some warnings to persist on early v1.x downloads and to fade as the
publisher accrues reputation. Signing is what makes accruing it possible; it is not an
instant switch.

## Hardening worth doing

- **Require approval to sign.** The signing values are already environment-scoped and the
  environment is restricted to `main` and `v*` tags. Under Settings → Environments → `release`
  you can also add **required reviewers**, so every signing run waits for a human. Left off
  because it makes each release, and each signing check, wait on a click; turn it on if more
  than one person can push to `main`.
- **Rotation and offboarding.** There is no private key to rotate. Revoking access means
  removing the role assignment or deleting the federated credential; do that when someone with
  subscription access leaves.
- **Watch the account.** Artifact Signing bills per signing operation above the included
  quota, so a compromised credential shows up as unexpected signing volume. Check current
  [pricing](https://azure.microsoft.com/pricing/details/artifact-signing/) before budgeting.
