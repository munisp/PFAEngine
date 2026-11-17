//! Structured JSON errors: {"error":{"code":"...","message":"..."}}.

use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::Json;
use serde_json::json;

#[derive(Debug)]
pub struct ApiError {
    pub status: StatusCode,
    pub code: &'static str,
    pub message: String,
}

impl ApiError {
    pub fn new(status: StatusCode, code: &'static str, message: impl Into<String>) -> Self {
        ApiError { status, code, message: message.into() }
    }

    pub fn bad_key(msg: impl Into<String>) -> Self {
        Self::new(StatusCode::BAD_REQUEST, "invalid_key", msg)
    }

    pub fn unauthorized(msg: impl Into<String>) -> Self {
        Self::new(StatusCode::UNAUTHORIZED, "unauthorized", msg)
    }

    pub fn forbidden(msg: impl Into<String>) -> Self {
        Self::new(StatusCode::FORBIDDEN, "forbidden", msg)
    }

    pub fn not_found(msg: impl Into<String>) -> Self {
        Self::new(StatusCode::NOT_FOUND, "not_found", msg)
    }

    pub fn internal(msg: impl Into<String>) -> Self {
        Self::new(StatusCode::INTERNAL_SERVER_ERROR, "internal", msg)
    }
}

/// Maps an OpenDAL error: NotFound -> 404, everything else -> 500. The
/// backend error text is included at debug level only in the message; it
/// can carry backend metadata but never blob bytes.
pub fn op_err(context: &'static str, err: opendal::Error) -> ApiError {
    if err.kind() == opendal::ErrorKind::NotFound {
        ApiError::not_found(format!("{context}: object not found"))
    } else {
        tracing::warn!(%context, error = %err, "backend error");
        ApiError::internal(format!("{context}: backend error ({})", err.kind()))
    }
}

impl IntoResponse for ApiError {
    fn into_response(self) -> Response {
        (
            self.status,
            Json(json!({"error": {"code": self.code, "message": self.message}})),
        )
            .into_response()
    }
}
