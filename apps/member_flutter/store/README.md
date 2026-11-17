# Store metadata — member_flutter (native app)

The native Flutter app and the TWA (`apps/member-pwa/store/play/`) target the
same audience and the **same package id** (`com.pfaengine.member`) — ship only
one of them per package id on Google Play (see the decision matrix in
`docs/APP-STORE-RELEASE.md`). Listing copy is shared; only the deltas live here.

- `play/` — Google Play deltas vs the TWA listing
- `ios/` — App Store Connect checklist + privacy nutrition labels (Flutter is
  the only track that ships to iOS; the PWA is Android-only via Play)
