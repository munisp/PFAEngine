# TWA (Bubblewrap) — package the member PWA for Google Play

This directory holds the [Bubblewrap](https://github.com/GoogleChromeLabs/bubblewrap)
configuration that wraps `apps/member-pwa` in a Trusted Web Activity (TWA) so the
PWA ships on Google Play as `com.pfaengine.member` **without a browser URL bar**.

> Placeholder package id: `com.pfaengine.member`. Swap in the real org-owned
> application id before first upload — a Play package id can never be changed
> after the first upload.

## Prerequisites

- The PWA deployed at a public HTTPS origin (placeholder `https://app.pfaengine.example`)
  serving `manifest.webmanifest`, icons and the service worker. TWAs require HTTPS;
  HTTP origins cannot be verified.
- JDK 17+, Android SDK (Bubblewrap's `bubblewrap doctor` can install it).
- An upload signing keystore (see `docs/APP-STORE-RELEASE.md` for key generation).

## Build

```bash
npm install -g @bubblewrap/cli

# 1) Regenerate/refresh config against the live origin (first time or after
#    manifest changes), then merge any local edits back into twa-manifest.json:
bubblewrap init --manifest https://app.pfaengine.example/manifest.webmanifest

# 2) Build signed APK (testing) and AAB (Play upload):
bubblewrap build        # uses signingKey from twa-manifest.json
bubblewrap build --skipPwaValidation   # only when validating locally w/o public origin
```

Outputs land in this directory (`app-release-signed.apk`, `app-release-bundle.aab`).

## Digital Asset Links (required, or the URL bar shows)

1. Copy the SHA-256 fingerprint of the **Play app-signing certificate**
   (Play Console → Setup → App integrity → App signing key certificate) — *not*
   the upload key, when Play App Signing is enabled.
2. Put it in `../public/.well-known/assetlinks.json` (replace the placeholder)
   and redeploy the PWA so it is served at
   `https://<host>/.well-known/assetlinks.json` with content-type
   `application/json`, **publicly reachable with no auth and no redirects**.
3. Mirror the same fingerprint into `fingerprints` in `twa-manifest.json`.
4. Verify with `https://digitalassetlinks.googleapis.com/v1/statements:list?source.web.site=https://<host>&relation=delegate_permission/common.handle_all_urls`
   or the "Check app links" step in `bubblewrap validate`.

Until the fingerprint is deployed, the app falls back to a Custom Tab
(visible URL bar) — functional but not store-quality.

## Versioning

Bump `appVersionCode` (integer, monotonic) and `appVersionName` in
`twa-manifest.json` for every Play upload. Keep them in lockstep with the
semantic version tag that triggered the release (see
`docs/APP-STORE-RELEASE.md`).
