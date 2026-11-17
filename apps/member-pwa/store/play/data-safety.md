# Data safety form — draft answers (Play Console → App content → Data safety)

Aligned with the platform's NDPA posture: data minimisation, encryption in
transit and at rest, no sale of personal data, raw biometrics never leave the
device/server-side scoring only (the liveness video is used solely to compute
a liveness/verification score server-side; it is not repurposed, sold, or used
for advertising).

Global answers:

- **Does your app collect or share any of the required user data types?** Yes.
- **Is all user data encrypted in transit?** Yes (TLS 1.2+ only; TWA origin is
  HTTPS-only).
- **Do you provide a way for users to request that their data is deleted?**
  Yes — in-app support request and published privacy email. Note in the form
  that pension records are subject to statutory retention under the Pension
  Reform Act / PenCom regulations, so deletion is honoured where the law
  permits (account data) and declined with explanation where it does not
  (contribution history of an active RSA).
- **Committed to following Play's Families policy?** N/A — app is not directed
  at children.

## Data collected (per data type)

| Data type | Collected? | Shared? | Purpose | Optional? | Notes |
|---|---|---|---|---|---|
| Name | Yes | No | Account management, identity verification (KYC) | No | From enrolment records |
| Email address | Yes | No | Account management, security comms | Yes* | *Optional in form only if truly optional at onboarding |
| Phone number | Yes | No | Account management, OTP/step-up verification | No | — |
| National identity number (NIN) / BVN | Yes — declare under "Personal info → Other info" | No (shared only with identity-verification provider under contract, and PenCom as regulator — disclose both) | Identity verification (regulatory KYC) | No | Required by Nigerian pension regulation |
| Financial info → account balance & transaction history | Yes | No | App functionality (balance, statements, contributions) | No | Core product |
| Photos or videos → selfie/liveness video | Yes | No | Identity verification (I-Am-Alive, onboarding) | No | Ephemeral: encrypted in transit, scored server-side, not used for ads; retention per NDPA consent + privacy policy |
| Device or other IDs | Yes | No | Fraud prevention, security | No | — |
| App activity (pages viewed, taps) | Optional | No | Analytics, troubleshooting | Yes | Disable entirely if analytics module not enabled |
| Precise location | **No** | No | — | — | Not collected |
| Contacts, SMS, files, microphone, camera roll | No | No | — | — | Camera is used in-session only for liveness capture; declare camera permission usage, not media collection |

"Shared" column: nothing is shared with third parties for marketing/advertising.
Sharing with (a) the operating PFA (the data controller's own systems),
(b) contracted identity-verification processors, and (c) PenCom/regulators
where legally required — disclose these in the free-text/purpose fields and
the privacy policy.

## Honesty notes for the reviewer

- The app is a TWA of the member portal; all data flows go to the PFA's own
  HTTPS origin — there are no third-party trackers or ad SDKs bundled.
- If a tenant deployment adds analytics/crash-reporting SDKs later, this form
  must be revisited before release.
