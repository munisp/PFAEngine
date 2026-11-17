//! storage-svc — PFAEngine's object-storage microservice backed by Apache
//! OpenDAL.
//!
//! The Go services are CGO_ENABLED=0 static binaries, so the CGO-based
//! OpenDAL Go bindings are not an option. Instead this small Rust service
//! owns the real `opendal` crate and exposes a minimal, backend-agnostic
//! HTTP API:
//!
//!   PUT    /obj/{key}     store raw bytes            (bearer auth)
//!   GET    /obj/{key}     fetch raw bytes            (bearer auth, or
//!                         ?exp=..&sig=.. presigned)
//!   HEAD   /obj/{key}     metadata                   (same as GET)
//!   DELETE /obj/{key}     remove                     (bearer auth)
//!   POST   /presign       {"key","ttl_secs"} -> {"url","expires_at"}
//!                                                    (bearer auth)
//!   GET    /healthz       liveness                   (open)
//!
//! Presigned URLs are minted and verified by storage-svc itself
//! (HMAC-SHA256 over "GET\n<key>\n<exp>" with its own secret), so they work
//! uniformly across every OpenDAL backend — including fs and memory, which
//! have no native presign concept.
//!
//! See docs/OPENDAL.md for the architecture.

mod app;
mod error;
mod keys;
mod presign;

use std::net::SocketAddr;
use std::process::exit;

use tracing::{error, info, warn};

/// Runtime configuration, parsed from the environment (see README.md).
pub struct Config {
    pub listen_addr: SocketAddr,
    pub backend: String,
    pub fs_root: String,
    /// Bearer token required on mutating routes; None only in dev mode.
    pub token: Option<String>,
    /// HMAC secret for presigned URLs (falls back to the bearer token).
    pub presign_secret: Vec<u8>,
    /// External base URL used when minting presigned URLs.
    pub public_url: Option<String>,
    /// Max PUT body size in MiB.
    pub max_body_mib: usize,
}

fn env(key: &str) -> Option<String> {
    std::env::var(key).ok().map(|v| v.trim().to_string()).filter(|v| !v.is_empty())
}

fn fatal(msg: &str) -> ! {
    error!(%msg, "invalid configuration");
    eprintln!("storage-svc: FATAL: {msg}");
    exit(2)
}

impl Config {
    pub fn from_env() -> Config {
        let listen_addr = env("LISTEN_ADDR")
            .unwrap_or_else(|| "0.0.0.0:8110".to_string())
            .parse()
            .unwrap_or_else(|_| fatal("LISTEN_ADDR is not a valid socket address"));
        let backend = env("STORAGE_BACKEND").unwrap_or_else(|| "fs".to_string());
        let fs_root = env("FS_ROOT").unwrap_or_else(|| "/var/lib/storage-svc".to_string());

        let dev = matches!(
            env("STORAGE_ENV").as_deref(),
            Some("dev") | Some("development") | Some("test")
        );
        let token = env("STORAGE_TOKEN");
        if token.is_none() && !dev {
            fatal("STORAGE_TOKEN is unset outside dev mode — refusing to start \
                   (fail-closed). Set STORAGE_ENV=dev to run unauthenticated locally.");
        }
        if token.is_none() {
            warn!("STORAGE_TOKEN unset and STORAGE_ENV=dev — auth DISABLED; never do this in production");
        }

        let presign_secret = match env("STORAGE_PRESIGN_SECRET").or_else(|| token.clone()) {
            Some(s) => s.into_bytes(),
            None => {
                // Dev-only deterministic secret so URLs survive restarts in dev.
                warn!("STORAGE_PRESIGN_SECRET unset — using a deterministic dev-only signing secret");
                b"storage-svc-dev-insecure-secret".to_vec()
            }
        };

        let max_body_mib = env("STORAGE_MAX_BODY_MIB")
            .and_then(|v| v.parse().ok())
            .unwrap_or(64);

        Config {
            listen_addr,
            backend,
            fs_root,
            token,
            presign_secret,
            public_url: env("STORAGE_PUBLIC_URL"),
            max_body_mib,
        }
    }
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "storage_svc=info,tower_http=info".into()),
        )
        .json()
        .init();

    let cfg = Config::from_env();
    let op = match app::build_operator(&cfg) {
        Ok(op) => op,
        Err(e) => {
            error!(error = %e, backend = %cfg.backend, "failed to build OpenDAL operator");
            exit(2)
        }
    };
    info!(backend = %cfg.backend, addr = %cfg.listen_addr, "storage-svc starting");

    let router = app::router(app::AppState::new(op, &cfg));
    let listener = match tokio::net::TcpListener::bind(cfg.listen_addr).await {
        Ok(l) => l,
        Err(e) => {
            error!(%e, addr = %cfg.listen_addr, "bind failed");
            exit(2)
        }
    };
    if let Err(e) = axum::serve(listener, router)
        .with_graceful_shutdown(shutdown_signal())
        .await
    {
        error!(%e, "server error");
        exit(1)
    }
}

async fn shutdown_signal() {
    let _ = tokio::signal::ctrl_c().await;
    info!("shutdown signal received");
}
