# App Store Release Runbook — PFAEngine member apps

End-to-end guide for shipping the member apps to Google Play (TWA and/or
Flutter) and the Apple App Store (Flutter only). Competitive context: Stanbic
IBTC, ARM, Leadway and Trustfund all ship rated member apps — this runbook is
the path to parity.

> **Honest status (read first):** everything here is prepared but **nothing
> has been submitted**. Store submission requires organisation accounts and
> manual console steps that code cannot automate. The CI signing path is
> written but untested until real keystore secrets are configured. Placeholder
> values (`com.pfaengine.member`, `app.pfaengine.example`, demo listing copy)
> must be replaced with org-owned values before any upload.

---

## 1. Accounts & costs

| Account | Cost | Lead time | Notes |
|---|---|---|---|
| Google Play Console (org) | $25 one-time | Days — org verification needs D-U-N-S + documents | Create as **organisation**, not personal |
| Apple Developer Program (org) | $99/yr | Days–weeks — D-U-N-S required | Only needed for the Flutter iOS app |
| Play App Signing | Free | — | Enrol both apps; Google holds the app-signing key, you hold the upload key |
| Domain + HTTPS hosting for the PWA | existing | — | TWA hard-requires a public HTTPS origin |

Because PFAEngine is white-label, **each deploying PFA needs its own Play
Console / Apple Developer membership and its own package/bundle id** (tenant
rebrand: package id, app name, icons, listing). Budget accordingly.

## 2. Which app ships first: TWA vs Flutter decision matrix

| Criterion | PWA → TWA (`apps/member-pwa`) | Flutter (`apps/member_flutter`) |
|---|---|---|
| Time to Play listing | **Days** (app already exists, PWA is store-ready) | Weeks (scaffold, icons, polish, testing) |
| Code to maintain | Zero new app code | Full native codebase |
| Offline on patchy networks | Service worker: cached shell + last-known API data | Not yet implemented |
| Native camera/liveness UX | WebRTC in a TWA — works, but permission UX is clunkier | **Native camera plugin — better** |
| Push notifications | Web push via FCM (works in TWA) | Native FCM/APNs |
| iOS | **No** (Play/TWA is Android-only) | **Yes** |
| App size / low-end devices | ~3 MB wrapper | ~20+ MB, heavier runtime |
| Play discovery/ratings parity | Full — TWA is a first-class Play app | Full |

**Recommendation: ship the PWA as a TWA first** (Google Play covers >90% of
the Nigerian market; the member PWA is functionally ahead of the Flutter app),
then invest in Flutter when native features (robust camera liveness, push,
iOS) are demanded. Do **not** ship both under the same package id — a Play
package id maps to one signing identity and one listing; see
`apps/member_flutter/store/play/README.md` for the migration rules.

## 3. Signing keys (do this once, per deploying PFA)

```bash
# Upload key for Play (TWA and/or Flutter):
keytool -genkey -v -keystore upload-keystore.jks \
  -keyalg RSA -keysize 2048 -validity 10000 -alias pfaengine-upload

# SHA-256 fingerprint for assetlinks.json / Play App Signing cross-check:
keytool -list -v -keystore upload-keystore.jks -alias pfaengine-upload | grep SHA256
```

- Store the keystore + passwords in the org password manager; **never commit
  them** (`.gitignore` rules are in place, and `key.properties` is generated
  from a template).
- Enrol in **Play App Signing** at first upload. Afterwards the fingerprint
  that matters for Digital Asset Links is the **app-signing certificate** shown
  in Play Console → Setup → App integrity — *not* the upload key's.
- CI: add `KEYSTORE_BASE64` (`base64 -w0 upload-keystore.jks`),
  `KEYSTORE_PASSWORD`, `KEY_ALIAS`, `KEY_PASSWORD` as GitHub Actions secrets.

## 4. Track A — PWA → Google Play via TWA (fastest)

1. Deploy `apps/member-pwa` at the production HTTPS origin; confirm
   `/manifest.webmanifest`, `/icons/icon-512.png`, `/sw.js` and
   `/.well-known/assetlinks.json` are all served over HTTPS with no auth.
2. Put the Play app-signing SHA-256 into
   `apps/member-pwa/public/.well-known/assetlinks.json` (replace the
   placeholder) and into `twa/twa-manifest.json` → `fingerprints`.
3. `npm i -g @bubblewrap/cli`, then follow `apps/member-pwa/twa/README.md`
   (`bubblewrap init` against the live origin, `bubblewrap build`).
4. Play Console: create app (package id `com.pfaengine.member` or tenant id),
   upload the AAB to internal track, fill the listing from
   `apps/member-pwa/store/play/` (title, descriptions, content rating,
   data-safety, screenshots, feature graphic), complete the Financial
   features declaration with the PFA's PenCom licence.
5. Internal → closed → production rollout (staged: 10% → 50% → 100%).

## 5. Track B — Flutter native

1. Generate the platform scaffolds + release config:
   ```bash
   bash scripts/flutter-release-scaffold.sh          # idempotent; safe to re-run
   bash scripts/flutter-release-scaffold.sh --check  # dry-run
   ```
   This runs `flutter create --platforms=android,ios --org com.pfaengine .`
   (skipped when `android/` exists) and copies the release config from
   `apps/member_flutter/release-config/` (signing-aware `build.gradle`,
   ProGuard rules, hardened `AndroidManifest.xml`, `key.properties.template`).
2. Replace the default launcher icons with tenant brand assets (adaptive
   icons for Android; 1024×1024 for App Store). The default Flutter icon must
   not ship.
3. Local release build:
   ```bash
   cd apps/member_flutter
   cp android/key.properties.template android/key.properties   # fill it in
   flutter build appbundle --release --obfuscate --split-debug-info=build/symbols
   ```
4. Play listing: reuse `apps/member-pwa/store/play/` copy with the deltas in
   `apps/member_flutter/store/play/README.md`.
5. iOS (Mac + Xcode required): follow
   `apps/member_flutter/store/ios/app-store-connect-checklist.md` — bundle id,
   `NSCameraUsageDescription` (automatic rejection without it), screenshots,
   privacy nutrition labels (`privacy-nutrition-labels.md`), export compliance.

## 6. CI (`.github/workflows/release-app.yml`)

Triggered on tags `v*.*.*` (or manual dispatch):

- **pwa** — `npm install` + `npm run build`, uploads `dist/`.
  (No `package-lock.json` exists for member-pwa yet; commit one and switch to
  `npm ci` before relying on CI builds for releases.)
- **twa** — Bubblewrap build; **disabled until repo variable
  `TWA_HOST_CONFIGURED=true`** because Bubblewrap downloads icons from the
  live origin. Signed when keystore secrets exist, unsigned otherwise.
- **flutter-android** — runs the scaffold script, configures signing from
  secrets, builds obfuscated AAB + APK with split debug symbols, uploads
  artifacts. A commented fastlane `supply` step shows the Play internal-track
  upload (needs a Play service account JSON in `PLAY_SERVICE_ACCOUNT_JSON`).

Known CI caveats: the signing path is untested without real secrets; the TWA
job's `bubblewrap update` step may need a first local `bubblewrap init` run
committed; iOS builds need a macOS runner and are intentionally not wired up.

## 7. Version strategy

- One semver for the member experience, applied as a git tag `vX.Y.Z`.
- **PWA**: version is deploy-time; no store version.
- **TWA**: `appVersionName = X.Y.Z`, `appVersionCode` = monotonically
  increasing integer (Play requirement). Suggested encoding:
  `X*10000 + Y*100 + Z` (1.0.0 → 10000). Bump per upload.
- **Flutter**: `version: X.Y.Z+<code>` in `pubspec.yaml` (Flutter derives
  `versionCode` from `+<code>`).
- Tag → CI builds → artifacts attached to the workflow run; only promote
  artifacts built from the tagged commit.

## 8. Review-time expectations

| Store | Typical | Financial-app reality |
|---|---|---|
| Google Play | 3–7 days (first app) | Financial features declaration + PenCom licence proof can add 1–2 weeks |
| Apple App Store | 24–48 h per review | Guideline 5.2.3 (financial licensing) queries are common for first submission; respond via App Review with licence documents |

Plan launch comms for **at least 3–4 weeks** of store latency on the first
submission of either app.

## 9. Pre-submission checklist (both tracks)

- [ ] Org Play Console / Apple Developer accounts verified
- [ ] Placeholders replaced: package id, host, fingerprints, listing branding
- [ ] `assetlinks.json` live at `/.well-known/assetlinks.json` (TWA)
- [ ] Privacy policy URL live, NDPA-compliant, matching data-safety answers
- [ ] Screenshots from the **actual artifact being uploaded**
- [ ] Financial features declaration + PenCom licence ready
- [ ] Release notes written; staged rollout configured
