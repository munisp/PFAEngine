//! Router, shared state, and handlers.

use axum::body::Bytes;
use axum::extract::{DefaultBodyLimit, Path, Query, State};
use axum::http::{header, HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use opendal::Operator;
use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::error::{op_err, ApiError};
use crate::keys::{escape_key, sanitize_key};
use crate::presign::Presigner;
use crate::Config;

#[derive(Clone)]
pub struct AppState {
    op: Operator,
    token: Option<String>,
    signer: Presigner,
    public_url: Option<String>,
    backend: String,
    max_body_bytes: usize,
}

impl AppState {
    pub fn new(op: Operator, cfg: &Config) -> Self {
        AppState {
            op,
            token: cfg.token.clone(),
            signer: Presigner::new(cfg.presign_secret.clone()),
            public_url: cfg.public_url.clone(),
            backend: cfg.backend.clone(),
            max_body_bytes: cfg.max_body_mib.saturating_mul(1024 * 1024),
        }
    }

    /// Bearer-auth gate for routes that are not covered by a valid
    /// presigned URL. Fails closed whenever a token is configured.
    fn require_auth(&self, headers: &HeaderMap) -> Result<(), ApiError> {
        let Some(expected) = &self.token else {
            return Ok(()); // dev mode (logged loudly at startup)
        };
        let ok = headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.strip_prefix("Bearer "))
            .map(|presented| constant_time_eq(presented.as_bytes(), expected.as_bytes()))
            .unwrap_or(false);
        if ok {
            Ok(())
        } else {
            Err(ApiError::unauthorized("missing or invalid bearer token"))
        }
    }
}

/// Extractor enforcing bearer auth BEFORE the body is parsed, so an
/// unauthenticated request is rejected with 401 regardless of payload
/// shape (rather than a body-extraction error leaking through first).
pub struct RequireAuth;

#[axum::async_trait]
impl axum::extract::FromRequestParts<AppState> for RequireAuth {
    type Rejection = ApiError;

    async fn from_request_parts(
        parts: &mut axum::http::request::Parts,
        state: &AppState,
    ) -> Result<Self, Self::Rejection> {
        state.require_auth(&parts.headers)?;
        Ok(RequireAuth)
    }
}

fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

/// Builds the OpenDAL operator for the configured backend. Extra backends
/// (s3/gcs/azblob) are compile-time features; asking for one that was not
/// compiled in is a hard error, never a silent fallback.
pub fn build_operator(cfg: &Config) -> Result<Operator, opendal::Error> {
    let op = match cfg.backend.as_str() {
        "fs" => {
            let b = opendal::services::Fs::default().root(&cfg.fs_root);
            Operator::new(b)?.finish()
        }
        "memory" => {
            let b = opendal::services::Memory::default();
            Operator::new(b)?.finish()
        }
        #[cfg(feature = "s3")]
        "s3" => {
            let mut b = opendal::services::S3::default();
            if let Some(v) = crate::env("S3_BUCKET") { b = b.bucket(&v); }
            if let Some(v) = crate::env("S3_ENDPOINT") { b = b.endpoint(&v); }
            if let Some(v) = crate::env("S3_REGION") { b = b.region(&v); }
            if let Some(v) = crate::env("S3_ACCESS_KEY") { b = b.access_key_id(&v); }
            if let Some(v) = crate::env("S3_SECRET_KEY") { b = b.secret_access_key(&v); }
            Operator::new(b)?.finish()
        }
        #[cfg(feature = "gcs")]
        "gcs" => {
            let mut b = opendal::services::Gcs::default();
            if let Some(v) = crate::env("GCS_BUCKET") { b = b.bucket(&v); }
            if let Some(v) = crate::env("GCS_CREDENTIAL") { b = b.credential(&v); }
            if let Some(v) = crate::env("GCS_ENDPOINT") { b = b.endpoint(&v); }
            Operator::new(b)?.finish()
        }
        #[cfg(feature = "azblob")]
        "azblob" => {
            let mut b = opendal::services::Azblob::default();
            if let Some(v) = crate::env("AZBLOB_CONTAINER") { b = b.container(&v); }
            if let Some(v) = crate::env("AZBLOB_ACCOUNT_NAME") { b = b.account_name(&v); }
            if let Some(v) = crate::env("AZBLOB_ACCOUNT_KEY") { b = b.account_key(&v); }
            if let Some(v) = crate::env("AZBLOB_ENDPOINT") { b = b.endpoint(&v); }
            Operator::new(b)?.finish()
        }
        other => {
            let msg = if matches!(other, "s3" | "gcs" | "azblob") {
                format!(
                    "STORAGE_BACKEND {other:?} was not compiled in — rebuild with `--features {other}`"
                )
            } else {
                format!("unknown STORAGE_BACKEND {other:?}")
            };
            return Err(opendal::Error::new(opendal::ErrorKind::ConfigInvalid, msg));
        }
    };
    Ok(op)
}

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .route("/presign", post(presign_issue))
        .route(
            "/obj/*key",
            get(obj_get).head(obj_head).put(obj_put).delete(obj_delete),
        )
        .layer(DefaultBodyLimit::max(state.max_body_bytes))
        .with_state(state)
}

async fn healthz(State(st): State<AppState>) -> Json<serde_json::Value> {
    Json(json!({"status": "ok", "backend": st.backend}))
}

// ---- /obj/{key} ----

#[derive(Deserialize)]
pub struct PresignQuery {
    exp: Option<u64>,
    sig: Option<String>,
}

/// GET auth rule: a valid presigned query suffices; otherwise bearer.
fn read_auth(st: &AppState, headers: &HeaderMap, key: &str, q: &PresignQuery) -> Result<(), ApiError> {
    if let (Some(exp), Some(sig)) = (q.exp, q.sig.as_deref()) {
        let now = now_secs();
        return st.signer.verify(key, exp, sig, now);
    }
    st.require_auth(headers)
}

async fn obj_get(
    State(st): State<AppState>,
    Path(key): Path<String>,
    headers: HeaderMap,
    Query(q): Query<PresignQuery>,
) -> Result<Response, ApiError> {
    let key = sanitize_key(&key)?;
    read_auth(&st, &headers, &key, &q)?;
    let buf = st.op.read(&key).await.map_err(|e| op_err("get", e))?;
    Ok((
        [(header::CONTENT_TYPE, "application/octet-stream")],
        buf.to_vec(),
    )
        .into_response())
}

async fn obj_head(
    State(st): State<AppState>,
    Path(key): Path<String>,
    headers: HeaderMap,
    Query(q): Query<PresignQuery>,
) -> Result<Response, ApiError> {
    let key = sanitize_key(&key)?;
    read_auth(&st, &headers, &key, &q)?;
    let meta = st.op.stat(&key).await.map_err(|e| op_err("head", e))?;
    Ok((
        [(header::CONTENT_LENGTH, meta.content_length().to_string())],
        StatusCode::OK,
    )
        .into_response())
}

async fn obj_put(
    State(st): State<AppState>,
    Path(key): Path<String>,
    _auth: RequireAuth,
    body: Bytes,
) -> Result<Response, ApiError> {
    let key = sanitize_key(&key)?;
    st.op.write(&key, body).await.map_err(|e| op_err("put", e))?;
    Ok((StatusCode::OK, Json(json!({"ok": true, "key": key}))).into_response())
}

async fn obj_delete(
    State(st): State<AppState>,
    Path(key): Path<String>,
    _auth: RequireAuth,
) -> Result<Response, ApiError> {
    let key = sanitize_key(&key)?;
    match st.op.delete(&key).await {
        Ok(()) => Ok((StatusCode::OK, Json(json!({"ok": true, "key": key}))).into_response()),
        // Delete is idempotent: a missing object is already deleted.
        Err(e) if e.kind() == opendal::ErrorKind::NotFound => {
            Ok((StatusCode::OK, Json(json!({"ok": true, "key": key}))).into_response())
        }
        Err(e) => Err(op_err("delete", e)),
    }
}

// ---- /presign ----

#[derive(Deserialize)]
pub struct PresignRequest {
    key: String,
    ttl_secs: u64,
}

#[derive(Serialize)]
pub struct PresignResponse {
    url: String,
    expires_at: u64,
}

async fn presign_issue(
    State(st): State<AppState>,
    _auth: RequireAuth,
    headers: HeaderMap,
    Json(req): Json<PresignRequest>,
) -> Result<Json<PresignResponse>, ApiError> {
    let key = sanitize_key(&req.key)?;
    let (exp, sig) = st.signer.sign(&key, req.ttl_secs, now_secs())?;
    let base = st
        .public_url
        .clone()
        .map(|u| u.trim_end_matches('/').to_string())
        .or_else(|| host_base(&headers))
        .ok_or_else(|| {
            ApiError::internal("cannot determine public base URL; set STORAGE_PUBLIC_URL")
        })?;
    Ok(Json(PresignResponse {
        url: format!("{base}/obj/{}?exp={exp}&sig={sig}", escape_key(&key)),
        expires_at: exp,
    }))
}

fn host_base(headers: &HeaderMap) -> Option<String> {
    let host = headers
        .get("x-forwarded-host")
        .or_else(|| headers.get(header::HOST))?
        .to_str()
        .ok()?;
    let proto = headers
        .get("x-forwarded-proto")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("http");
    Some(format!("{proto}://{host}"))
}

fn now_secs() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use axum::http::Request;
    use http_body_util::BodyExt;
    use tower::ServiceExt;

    const TOKEN: &str = "test-token";

    fn test_config() -> Config {
        test_config_for("memory")
    }

    fn test_config_for(backend: &str) -> Config {
        Config {
            listen_addr: "127.0.0.1:0".parse().unwrap(),
            backend: backend.to_string(),
            fs_root: "/tmp/unused".to_string(),
            token: Some(TOKEN.to_string()),
            presign_secret: b"router-test-secret".to_vec(),
            public_url: Some("http://storage.test".to_string()),
            max_body_mib: 8,
        }
    }

    fn test_router() -> Router {
        let cfg = test_config();
        let op = build_operator(&cfg).unwrap();
        router(AppState::new(op, &cfg))
    }

    fn authed(req: axum::http::request::Builder) -> axum::http::request::Builder {
        req.header(header::AUTHORIZATION, format!("Bearer {TOKEN}"))
    }

    async fn body_bytes(resp: Response) -> Bytes {
        resp.into_body().collect().await.unwrap().to_bytes()
    }

    async fn put(router: Router, key: &str, body: &'static [u8]) -> Response {
        router
            .oneshot(
                authed(Request::put(format!("/obj/{key}")))
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap()
    }

    #[tokio::test]
    async fn put_get_head_delete_roundtrip() {
        let r = test_router();

        let resp = put(r.clone(), "t1/doc/hello.bin", b"hello-bytes").await;
        assert_eq!(resp.status(), StatusCode::OK);

        let resp = r
            .clone()
            .oneshot(authed(Request::get("/obj/t1/doc/hello.bin")).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        assert_eq!(&body_bytes(resp).await[..], b"hello-bytes");

        let resp = r
            .clone()
            .oneshot(authed(Request::head("/obj/t1/doc/hello.bin")).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        assert_eq!(
            resp.headers().get(header::CONTENT_LENGTH).unwrap(),
            &"11".to_string()
        );

        let resp = r
            .clone()
            .oneshot(authed(Request::delete("/obj/t1/doc/hello.bin")).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);

        let resp = r
            .clone()
            .oneshot(authed(Request::get("/obj/t1/doc/hello.bin")).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::NOT_FOUND);
        let body = body_bytes(resp).await;
        let v: serde_json::Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(v["error"]["code"], "not_found");

        // delete is idempotent
        let resp = r
            .oneshot(authed(Request::delete("/obj/t1/doc/hello.bin")).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
    }

    #[tokio::test]
    async fn mutating_routes_require_auth() {
        let r = test_router();
        let resp = r
            .clone()
            .oneshot(Request::put("/obj/a/b").body(Body::from("x")).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::UNAUTHORIZED);
        let body = body_bytes(resp).await;
        let v: serde_json::Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(v["error"]["code"], "unauthorized");

        let resp = r
            .clone()
            .oneshot(
                Request::delete("/obj/a/b")
                    .header(header::AUTHORIZATION, "Bearer wrong")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::UNAUTHORIZED);

        let resp = r
            .clone()
            .oneshot(Request::post("/presign").body(Body::from("{}")).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn traversal_keys_rejected() {
        let r = test_router();
        // raw and percent-encoded traversal
        for path in ["/obj/../x", "/obj/a/../../b", "/obj/%2e%2e/secret", "/obj/a//b"] {
            let resp = r
                .clone()
                .oneshot(authed(Request::put(path)).body(Body::from("x")).unwrap())
                .await
                .unwrap();
            assert!(
                resp.status() == StatusCode::BAD_REQUEST
                    || resp.status() == StatusCode::NOT_FOUND, // axum rejects ".."
                "{path} -> {}",
                resp.status()
            );
        }
    }

    #[tokio::test]
    async fn presign_issue_validate_expiry_tamper() {
        let r = test_router();
        let resp = put(r.clone(), "docs/report.pdf", b"pdf-bytes").await;
        assert_eq!(resp.status(), StatusCode::OK);

        // issue
        let resp = r
            .clone()
            .oneshot(
                authed(Request::post("/presign"))
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from(r#"{"key":"docs/report.pdf","ttl_secs":600}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        let body = body_bytes(resp).await;
        let v: serde_json::Value = serde_json::from_slice(&body).unwrap();
        let url = v["url"].as_str().unwrap();
        assert!(url.starts_with("http://storage.test/obj/docs/report.pdf?exp="));
        assert!(v["expires_at"].as_u64().unwrap() > now_secs());

        // redeem WITHOUT bearer auth — the signature is the credential
        let path = url.strip_prefix("http://storage.test").unwrap();
        let resp = r
            .clone()
            .oneshot(Request::get(path).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        assert_eq!(&body_bytes(resp).await[..], b"pdf-bytes");

        // tampered key: signature must not transfer to another object
        let tampered = path.replace("docs/report.pdf", "docs/other.pdf");
        let resp = r
            .clone()
            .oneshot(Request::get(tampered).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::FORBIDDEN);

        // tampered signature
        let bad_sig = format!("{path}x");
        let resp = r
            .clone()
            .oneshot(Request::get(bad_sig).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::FORBIDDEN);

        // expired: mint a signature for a past expiry with the same secret
        let signer = Presigner::new(b"router-test-secret".to_vec());
        let exp = now_secs() - 10;
        let (_, sig) = {
            // sign with ttl 1 at (exp-1) so it is already expired
            let (_, s) = signer.sign("docs/report.pdf", 1, exp - 1).unwrap();
            (exp, s)
        };
        let resp = r
            .oneshot(
                Request::get(format!("/obj/docs/report.pdf?exp={exp}&sig={sig}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::FORBIDDEN);

        // presign validates ttl bounds
        let r2 = test_router();
        let resp = r2
            .oneshot(
                authed(Request::post("/presign"))
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from(r#"{"key":"a","ttl_secs":0}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::BAD_REQUEST);
    }

    #[tokio::test]
    async fn healthz_is_open() {
        let r = test_router();
        let resp = r
            .oneshot(Request::get("/healthz").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        let body = body_bytes(resp).await;
        let v: serde_json::Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(v["status"], "ok");
        assert_eq!(v["backend"], "memory");
    }

    #[tokio::test]
    async fn fs_backend_roundtrip() {
        let dir = std::env::temp_dir().join(format!(
            "storage-svc-test-{}-{}",
            std::process::id(),
            now_secs()
        ));
        let mut cfg = test_config_for("fs");
        cfg.fs_root = dir.to_string_lossy().to_string();
        let op = build_operator(&cfg).unwrap();
        let r = router(AppState::new(op, &cfg));

        let resp = put(r.clone(), "k/v.bin", b"fs-data").await;
        assert_eq!(resp.status(), StatusCode::OK);
        let resp = r
            .oneshot(authed(Request::get("/obj/k/v.bin")).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(resp.status(), StatusCode::OK);
        assert_eq!(&body_bytes(resp).await[..], b"fs-data");
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn s3_without_feature_is_a_hard_error() {
        let cfg = test_config_for("s3");
        let err = build_operator(&cfg).unwrap_err();
        assert_eq!(err.kind(), opendal::ErrorKind::ConfigInvalid);
    }
}
