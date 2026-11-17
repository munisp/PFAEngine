# App privacy nutrition labels — draft (App Store Connect)

Same NDPA posture as the Play data-safety form: minimisation, encryption in
transit, no tracking, no sale of data, raw biometrics never leave the
device/server-side scoring only.

Global answers:

- **Data used to track you** (across other companies' apps/websites): **None.**
  No advertising SDKs, no cross-app identifiers. ATT prompt not needed.
- **Data linked to the user**: yes, see table.

| Apple data type | Collected | Linked to user | Purpose |
|---|---|---|---|
| Contact Info → Name | Yes | Yes | App functionality, identity verification |
| Contact Info → Email | Yes | Yes | Account management |
| Contact Info → Phone number | Yes | Yes | Account management, security (OTP) |
| Identifiers → User ID (RSA PIN / member id) | Yes | Yes | App functionality |
| Identifiers → Device ID | Yes | Yes | Security & fraud prevention |
| Sensitive Info → government ID data (NIN/BVN) | Yes | Yes | Identity verification (regulatory KYC) |
| Financial Info → payment/account info (RSA balance, contributions, transactions) | Yes | Yes | App functionality |
| Photos or Videos (selfie liveness capture) | Yes | Yes | Identity verification — liveness scored server-side; not used for any other purpose; retention per NDPA consent |
| User Content → customer support messages | Yes | Yes | Customer support |
| Diagnostics → crash data / performance | Only if crash reporting is enabled | No (not linked) | App functionality, stability |
| Location, contacts, browsing/search history, health data, advertising data | **No** | — | — |

Notes:

- "Collected" means transmitted off the device. On-device-only data (secure
  storage of session tokens via Keychain) is not a privacy-label disclosure.
- Biometric processing: liveness video is transmitted encrypted, scored
  server-side, and the raw media is handled per the privacy policy; the
  stored artefact is the verification result. Describe exactly this in the
  label's purpose text and the privacy policy.
- Keep labels in sync with the Play data-safety form
  (`../../member-pwa/store/play/data-safety.md`) — both describe the same
  backend data flows. Re-validate whenever a plugin/SDK is added to
  `pubspec.yaml`.
