# App Store Connect checklist — PFAEngine Member (iOS)

Prerequisite: Apple Developer Program membership for the operating PFA
(**$99/yr**, organisation account — requires D-U-N-S number; enrolment takes
days–weeks, start early).

## One-time setup

- [ ] Organisation Apple Developer account active; App Store Connect access
      for release manager (App Manager role minimum).
- [ ] Bundle ID registered: `com.pfaengine.member` (explicit, no wildcard).
- [ ] Distribution certificate + provisioning profile — use **Xcode "Automatically
      manage signing"** or fastlane match; store the private key in the org
      password manager, not in git.
- [ ] App Store Connect app record created: name `PFAEngine Member — My RSA`
      (30 chars), primary language English (Nigeria), SKU `pfaengine-member`,
      category **Finance**, secondary **Lifestyle** or none.
- [ ] Privacy policy URL (same NDPA-compliant page as Play).
- [ ] Support URL + marketing URL (marketing optional).

## Per-release checklist

- [ ] `version:` bumped in `apps/member_flutter/pubspec.yaml`
      (`major.minor.patch+build` → CFBundleShortVersionString + CFBundleVersion).
- [ ] `flutter build ipa --release --obfuscate --split-debug-info=build/symbols`
      (requires macOS + Xcode; CI would need a macOS runner — not wired up yet).
- [ ] Upload via Xcode Organizer, Transporter, or `xcrun altool`; wait for
      processing, fix any missing-compliance flags.
- [ ] Export compliance: app uses only standard TLS/HTTPS → answer **"uses
      exempt encryption"** (ITSAppUsesNonExemptEncryption can be set to false
      in Info.plist to skip the per-build prompt).
- [ ] Screenshots (required sizes): 6.9" (1320×2868) and 6.5" (1284×2778)
      iPhone; 12.9" iPad (2048×2732) if supporting iPad. Same shot list as
      `../play/README.md` → member-pwa screenshot checklist.
- [ ] Age rating questionnaire: expect **4+** (no restricted content;
      financial apps still answer the full questionnaire).
- [ ] Privacy nutrition labels: see `privacy-nutrition-labels.md`.
- [ ] Review notes for App Review: demo RSA credentials (sandbox account),
      note that real NIN/BVN verification is stubbed/sandboxed in demo mode,
      contact info, and that the app manages regulated pension accounts
      (attach PenCom licence of the operating PFA — Apple frequently asks
      financial apps for licensing proof under guideline 5.2.3/3.1.1).
- [ ] Submit; typical review time 24–48 h (first submission of a financial
      app can take 1–2 weeks with licensing queries).

## Known gaps before first submission

- No `ios/` scaffold in repo yet — run `scripts/flutter-release-scaffold.sh`
  (or `flutter create --platforms=ios .`) on a Mac, then set the bundle id,
  display name, and camera usage description (`NSCameraUsageDescription`:
  "Used to verify your identity during account setup and I-Am-Alive checks.")
  in Xcode. Camera without a usage description is an automatic rejection.
- iOS icons must be generated from brand assets (no alpha channel, 1024×1024
  App Store icon).
