//! Backend-independent presigned GET URLs.
//!
//! storage-svc mints and verifies the URLs itself — HMAC-SHA256 over
//! "GET\n<key>\n<expiry-unix>" keyed by its own secret (STORAGE_PRESIGN_
//! SECRET, falling back to STORAGE_TOKEN). This works uniformly for every
//! OpenDAL backend (fs, memory, s3, gcs, azblob), unlike native presign
//! which only some object stores support. The trade-off: the URL is served
//! by storage-svc, so it must be reachable by whoever redeems it.

use hmac::{Hmac, Mac};
use sha2::Sha256;

use crate::error::ApiError;

type HmacSha256 = Hmac<Sha256>;

/// Maximum presign TTL: 7 days (mirrors SigV4).
pub const MAX_TTL_SECS: u64 = 7 * 24 * 3600;

#[derive(Clone)]
pub struct Presigner {
    secret: Vec<u8>,
}

impl Presigner {
    pub fn new(secret: Vec<u8>) -> Self {
        Presigner { secret }
    }

    fn mac(&self, key: &str, exp: u64) -> Vec<u8> {
        let mut m = HmacSha256::new_from_slice(&self.secret).expect("HMAC accepts any key size");
        m.update(b"GET\n");
        m.update(key.as_bytes());
        m.update(b"\n");
        m.update(exp.to_string().as_bytes());
        m.finalize().into_bytes().to_vec()
    }

    /// Issues (expiry, hex signature) for a GET on `key` valid for ttl.
    pub fn sign(&self, key: &str, ttl_secs: u64, now: u64) -> Result<(u64, String), ApiError> {
        if ttl_secs == 0 || ttl_secs > MAX_TTL_SECS {
            return Err(ApiError::bad_key(format!(
                "ttl_secs must be 1..={MAX_TTL_SECS}, got {ttl_secs}"
            )));
        }
        let exp = now + ttl_secs;
        Ok((exp, hex::encode(self.mac(key, exp))))
    }

    /// Verifies a presented (expiry, signature) pair for `key`.
    pub fn verify(&self, key: &str, exp: u64, sig_hex: &str, now: u64) -> Result<(), ApiError> {
        if now > exp {
            return Err(ApiError::forbidden("presigned URL expired"));
        }
        let presented = hex::decode(sig_hex)
            .map_err(|_| ApiError::forbidden("malformed signature"))?;
        let mut m = HmacSha256::new_from_slice(&self.secret).expect("HMAC accepts any key size");
        m.update(b"GET\n");
        m.update(key.as_bytes());
        m.update(b"\n");
        m.update(exp.to_string().as_bytes());
        m.verify_slice(&presented)
            .map_err(|_| ApiError::forbidden("invalid signature"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_and_tamper() {
        let p = Presigner::new(b"test-secret".to_vec());
        let (exp, sig) = p.sign("a/b.txt", 300, 1_000).unwrap();
        assert_eq!(exp, 1_300);
        assert!(p.verify("a/b.txt", exp, &sig, 1_000).is_ok());
        assert!(p.verify("a/b.txt", exp, &sig, 1_300).is_ok());
        // wrong key, wrong secret, expired, garbage sig all fail
        assert!(p.verify("a/other.txt", exp, &sig, 1_000).is_err());
        assert!(p.verify("a/b.txt", exp, &sig, 1_301).is_err());
        assert!(p.verify("a/b.txt", exp, "deadbeef", 1_000).is_err());
        let q = Presigner::new(b"other-secret".to_vec());
        assert!(q.verify("a/b.txt", exp, &sig, 1_000).is_err());
    }

    #[test]
    fn ttl_bounds() {
        let p = Presigner::new(b"s".to_vec());
        assert!(p.sign("k", 0, 0).is_err());
        assert!(p.sign("k", MAX_TTL_SECS + 1, 0).is_err());
        assert!(p.sign("k", MAX_TTL_SECS, 0).is_ok());
    }
}
