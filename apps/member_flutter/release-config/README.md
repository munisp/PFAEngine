# Flutter release config templates

These files are copied into the generated `android/` tree by
`../../../scripts/flutter-release-scaffold.sh`. They are kept here (instead of
a committed `android/` scaffold) so `flutter create` always owns the baseline
and upgrades cleanly; only the release-specific deltas live in this repo.

Layout mirrors the copy destination under `apps/member_flutter/`:

| Source (this dir) | Destination | Purpose |
|---|---|---|
| `android/app/build.gradle` | `android/app/build.gradle` | Release signing + R8/ProGuard config (replaces generated file) |
| `android/app/proguard-rules.pro` | `android/app/proguard-rules.pro` | Keep rules for Flutter + plugins |
| `android/app/src/main/AndroidManifest.xml` | same | Label, permissions, hardening |
| `android/key.properties.template` | `android/key.properties.template` | Keystore config template (copy to `key.properties`, fill locally — never commit) |

Application id: `com.pfaengine.member` (same as the TWA — pick ONE artifact
per package id on Play; see `docs/APP-STORE-RELEASE.md` for the
TWA-vs-Flutter decision).

iOS release config (signing team, bundle id, capabilities) is set in Xcode /
App Store Connect and documented in `../store/ios/app-store-connect-checklist.md`;
there are no file-level iOS templates here.
