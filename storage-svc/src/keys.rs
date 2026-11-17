//! Object-key sanitization. Keys are lake/object paths relative to the
//! backend root; anything that could escape the root is rejected outright
//! (fail-closed, never silently rewritten).

use crate::error::ApiError;

pub const MAX_KEY_LEN: usize = 1024;

/// Validates a key from the URL path. Rejects empty keys, absolute paths,
/// backslashes, "." / ".." / empty segments, control characters.
pub fn sanitize_key(key: &str) -> Result<String, ApiError> {
    if key.is_empty() {
        return Err(ApiError::bad_key("empty key"));
    }
    if key.len() > MAX_KEY_LEN {
        return Err(ApiError::bad_key("key exceeds 1024 bytes"));
    }
    if key.starts_with('/') || key.starts_with('\\') {
        return Err(ApiError::bad_key("absolute keys are not allowed"));
    }
    if key.chars().any(|c| c.is_control()) {
        return Err(ApiError::bad_key("key contains control characters"));
    }
    if key.contains('\\') {
        return Err(ApiError::bad_key("backslashes are not allowed in keys"));
    }
    for seg in key.split('/') {
        match seg {
            "" => return Err(ApiError::bad_key("key has an empty segment")),
            "." | ".." => {
                return Err(ApiError::bad_key("path traversal segments are not allowed"))
            }
            _ => {}
        }
    }
    Ok(key.to_string())
}

/// RFC 3986 per-segment percent-encoding for URLs we mint (presign).
/// Keys are already validated, so this is defense-in-depth.
pub fn escape_key(key: &str) -> String {
    let mut out = String::with_capacity(key.len());
    for b in key.bytes() {
        match b {
            b'a'..=b'z' | b'A'..=b'Z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' | b'/' => {
                out.push(b as char)
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_normal_keys() {
        for k in ["a", "a/b/c.txt", "tenant/doc-1/passport_photo.jpg", "x".repeat(1024).as_str()] {
            assert!(sanitize_key(k).is_ok(), "{k}");
        }
    }

    #[test]
    fn rejects_traversal_and_friends() {
        for k in [
            "",
            "/etc/passwd",
            "../x",
            "a/../b",
            "..",
            ".",
            "a//b",
            "a/",
            "a/./b",
            "a\\b",
            "a\u{0}b",
            &"x".repeat(1025),
        ] {
            assert!(sanitize_key(k).is_err(), "{k:?} must be rejected");
        }
    }

    #[test]
    fn escapes_unreserved_only() {
        assert_eq!(escape_key("a/b-c_d.e~f"), "a/b-c_d.e~f");
        assert_eq!(escape_key("a b/c+d"), "a%20b/c%2Bd");
    }
}
