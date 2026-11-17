# Google Play listing — Flutter app (deltas vs TWA listing)

Base copy is in `apps/member-pwa/store/play/` (title, short description, full
description, content rating, data safety, feature graphic). Use it unchanged
except for these deltas:

## Metadata deltas

- **App title**: same — `PFAEngine Member — My RSA` (or tenant brand).
- **Short / full description**: same. Optionally add one line to the full
  description: "Native app with secure on-device storage and camera-based
  identity verification."
- **Category**: Finance (same). **Content rating**: same answers
  (`../../member-pwa/store/play/content-rating.md`); the "web views" question
  becomes **No** (fully native UI).
- **Data safety**: same answers
  (`../../member-pwa/store/play/data-safety.md`) plus: camera permission is
  declared (in-session liveness capture only); `flutter_secure_storage`
  stores session tokens on-device (KeyStore-backed) — no extra data types.

## Asset deltas

- Screenshots must come from the **Flutter build** (native UI differs from the
  PWA). Same shot list and rules:
  `../../member-pwa/store/play/screenshot-checklist.md`, minus the "offline
  indicator" shot if the Flutter build doesn't ship offline caching yet —
  never screenshot a feature that isn't in the artifact being uploaded
  (Play rejects misleading listings).
- App icon: generate proper Android adaptive icons (foreground/background
  layers) and iOS icons from the tenant brand mark before release; the
  `flutter create` default icon is not shippable.
  (`dart run flutter_launcher_icons` once brand assets exist.)

## Package id warning

If the TWA already ships as `com.pfaengine.member`, the Flutter app must
either **replace** it (same package id, higher versionCode, migrate users via
normal app update — signing key must match Play App Signing enrolment) or use
a distinct id (e.g. `com.pfaengine.member.native`). Do not ship both.
