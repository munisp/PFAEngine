# Google Play listing — PFAEngine Member (com.pfaengine.member)

Locale: **en-NG** (primary). Category: **Finance**. App type: TWA wrapper of the
member PWA. Placeholder package id `com.pfaengine.member` — replace with the
org-owned id before first upload (package ids are immutable on Play).

| Field | File | Limit |
|---|---|---|
| App title | `title.txt` | 30 chars |
| Short description | `short-description.txt` | 80 chars |
| Full description | `full-description.txt` | 4000 chars |
| Content rating answers | `content-rating.md` | IARC questionnaire |
| Data safety form | `data-safety.md` | Play Console form |
| Screenshots | `screenshot-checklist.md` | min 2, phone 16:9/9:16 |
| Feature graphic | `feature-graphic-spec.md` | 1024×500 PNG/JPEG |

Also required in Play Console (not files here):

- **App icon**: 512×512 32-bit PNG (`../../public/icons/icon-512.png` — verify
  it looks acceptable as a maskable crop; regenerate brand assets before a
  real launch).
- **Privacy policy URL**: hosted page covering NDPA-compliant processing of
  identity (NIN/BVN), financial and biometric (liveness) data. Required —
  the app collects personal + financial data.
- **Target audience**: 18+ (pension product); declare "Not designed for
  children" in the Target Audience & Content section.
- **Declarations**: Financial features declaration (pension/retirement fund
  management — expect Play to request proof of PenCom licensing of the
  operating PFA; prepare licence + regulator letter in advance).
- **News app / COVID / government declarations**: No.
