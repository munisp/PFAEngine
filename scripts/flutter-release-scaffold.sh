#!/usr/bin/env bash
# flutter-release-scaffold.sh — generate the android/ + ios/ platform trees for
# apps/member_flutter and apply the release configuration.
#
# Idempotent: if android/ already exists, `flutter create` is skipped and only
# the release config files are (re)applied. Safe to re-run after Flutter SDK
# upgrades (`flutter create .` regenerates stale platform files on demand).
#
# Usage:
#   scripts/flutter-release-scaffold.sh            # scaffold + apply config
#   scripts/flutter-release-scaffold.sh --check    # dry-run: report what would happen
#
# Requires: flutter on PATH (stable channel, Dart >= 3.3 per pubspec).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
APP_DIR="${REPO_ROOT}/apps/member_flutter"
CONFIG_DIR="${APP_DIR}/release-config"
DRY_RUN=0

if [[ "${1:-}" == "--check" ]]; then
  DRY_RUN=1
fi

log() { printf '[scaffold] %s\n' "$*"; }
die() { printf '[scaffold] ERROR: %s\n' "$*" >&2; exit 1; }

run() {
  if [[ "${DRY_RUN}" -eq 1 ]]; then
    log "would run: $*"
  else
    "$@"
  fi
}

[[ -f "${APP_DIR}/pubspec.yaml" ]] || die "pubspec.yaml not found in ${APP_DIR}"
[[ -d "${CONFIG_DIR}/android" ]] || die "release-config/ missing in ${APP_DIR}"

# --- Step 1: generate platform scaffolds (skipped when android/ exists) -----
if [[ -d "${APP_DIR}/android" ]]; then
  log "android/ already exists — skipping 'flutter create' (idempotent)"
else
  if ! command -v flutter >/dev/null 2>&1; then
    if [[ "${DRY_RUN}" -eq 1 ]]; then
      log "flutter not on PATH (dry-run continues without it)"
    else
      die "flutter not on PATH. Install the Flutter SDK (stable) first: https://docs.flutter.dev/get-started/install"
    fi
  else
    log "flutter: $(flutter --version 2>/dev/null | head -n1)"
  fi
  log "generating android/ + ios/ scaffolds with flutter create"
  # --project-name must match the `name:` in pubspec.yaml; --org sets the
  # reverse-domain prefix (applicationId is overridden in build.gradle).
  run flutter create \
    --platforms=android,ios \
    --org com.pfaengine \
    --project-name pfaengine_member \
    "${APP_DIR}"
fi

# --- Step 2: apply release config -------------------------------------------
apply() {
  local src="$1" dst="$2"
  [[ -f "${src}" ]] || die "missing config file: ${src}"
  if [[ "${DRY_RUN}" -eq 1 ]]; then
    log "would copy ${src} -> ${dst}"
  else
    mkdir -p "$(dirname "${dst}")"
    cp "${src}" "${dst}"
    log "applied $(realpath --relative-to="${APP_DIR}" "${dst}")"
  fi
}

apply "${CONFIG_DIR}/android/app/build.gradle" \
      "${APP_DIR}/android/app/build.gradle"
apply "${CONFIG_DIR}/android/app/proguard-rules.pro" \
      "${APP_DIR}/android/app/proguard-rules.pro"
apply "${CONFIG_DIR}/android/app/src/main/AndroidManifest.xml" \
      "${APP_DIR}/android/app/src/main/AndroidManifest.xml"
apply "${CONFIG_DIR}/android/key.properties.template" \
      "${APP_DIR}/android/key.properties.template"

# --- Step 3: ensure secrets stay out of git ----------------------------------
GITIGNORE="${APP_DIR}/android/.gitignore"
GITIGNORE_LINES=(
  "key.properties"
  "*.jks"
  "*.keystore"
  "app/upload-keystore.jks"
)
if [[ "${DRY_RUN}" -eq 1 ]]; then
  log "would ensure ${GITIGNORE_LINES[*]} are in ${GITIGNORE}"
elif [[ -f "${GITIGNORE}" ]]; then
  for line in "${GITIGNORE_LINES[@]}"; do
    grep -qxF "${line}" "${GITIGNORE}" || echo "${line}" >> "${GITIGNORE}"
  done
  log "android/.gitignore covers keystores and key.properties"
else
  log "WARNING: ${GITIGNORE} not found — add key.properties / *.jks ignores manually"
fi

log "done. Next steps:"
cat <<'EOF'
  1) Create the upload keystore (once, keep it safe):
       keytool -genkey -v -keystore apps/member_flutter/android/app/upload-keystore.jks \
         -keyalg RSA -keysize 2048 -validity 10000 -alias pfaengine-upload
  2) Copy android/key.properties.template -> android/key.properties and fill it in.
  3) Build release artifacts:
       cd apps/member_flutter
       flutter build appbundle --release --obfuscate --split-debug-info=build/symbols
       flutter build apk --release --obfuscate --split-debug-info=build/symbols
  See docs/APP-STORE-RELEASE.md for the full runbook.
EOF
