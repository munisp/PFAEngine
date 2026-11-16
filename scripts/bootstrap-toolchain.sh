#!/bin/sh
# PFAEngine toolchain bootstrap — restores Go, Rust, Python env after a
# container restart. Archives live on the persistent mount
# (/mnt/agents/toolchains) because $HOME is ephemeral and /mnt/agents
# is mounted noexec (binaries must run from $HOME).
#
# Usage:  sh /mnt/agents/toolchains/bootstrap.sh
# Idempotent: skips anything already installed. Fully OFFLINE — everything
# restores from archives on the persistent mount (no network needed).

set -e
TC=/mnt/agents/toolchains

log() { echo "[bootstrap] $*"; }

# --- Go 1.23.4 -------------------------------------------------------------
if [ ! -x "$HOME/sdk/go/bin/go" ]; then
    log "restoring Go from persistent archive"
    mkdir -p "$HOME/sdk"
    tar -C "$HOME/sdk" -xzf "$TC/go1.23.4.linux-amd64.tar.gz"
fi
export PATH="$HOME/sdk/go/bin:$PATH"
log "go: $(go version)"

# --- Rust 1.83.0 (standalone, minimal profile) -----------------------------
# rustup-init segfaults in this sandbox; we restore the pre-installed
# standalone toolchain archive instead.
if [ ! -x "$HOME/rust/bin/cargo" ]; then
    log "restoring Rust from persistent archive"
    tar -C "$HOME" -xzf "$TC/rust-backup.tar.gz"
fi
export PATH="$HOME/rust/bin:$PATH"
log "cargo: $(cargo --version)"

# --- Python 3.12 -----------------------------------------------------------
# python3 ships in the base image at /usr/local/bin and survives restarts.
# User-installed pip packages do NOT; keep them in a persistent target dir.
export PYTHONUSERBASE="$TC/pylib"
mkdir -p "$PYTHONUSERBASE"
log "python3: $(python3 --version 2>/dev/null || echo missing) (PYTHONUSERBASE=$PYTHONUSERBASE)"

# --- Shared build env ------------------------------------------------------
# /mnt/agents is noexec -> build outputs must go to $HOME or /tmp.
export CARGO_TARGET_DIR=/tmp/cargo-target
export GOCACHE=${GOCACHE:-/tmp/gocache}
export GOPROXY=https://goproxy.cn,https://goproxy.io,direct
export GOSUMDB=off
export GOFLAGS=-mod=mod

log "done. Use: export PATH=$HOME/sdk/go/bin:$HOME/rust/bin:\$PATH"
