use std::{fs, path::Path};

use serde::{Deserialize, Serialize};

use crate::{ErrorCode, ResolvedPackage, ResolverError, decode_index, resolve};

/// Supported stdin/stdout JSON protocol version.
pub const PROTOCOL_VERSION: u32 = 1;
const MAX_ROOTS: usize = 10_000;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
/// One resolver sidecar request.
pub struct Request {
    /// Requested protocol version.
    pub protocol: u32,
    /// Filesystem path to `velocity.idx.zst`.
    pub index_path: String,
    /// Exact artifact target triple.
    pub target: String,
    /// Canonical package names or aliases to resolve.
    pub roots: Vec<String>,
}

#[derive(Debug, Serialize)]
#[serde(tag = "status", rename_all = "lowercase")]
/// One resolver sidecar response.
pub enum Response {
    /// Resolution succeeded.
    Ok {
        /// Response protocol version.
        protocol: u32,
        /// Selected packages in dependency-first order.
        packages: Vec<ResolvedPackage>,
    },
    /// Resolution failed.
    Error {
        /// Response protocol version.
        protocol: u32,
        /// Structured error details.
        error: WireError,
    },
}

#[derive(Debug, Serialize)]
/// Machine-readable error response body.
pub struct WireError {
    /// Stable error category.
    pub code: ErrorCode,
    /// Human-readable error context.
    pub message: String,
}

impl Response {
    /// Converts a resolver failure to the protocol error response.
    pub fn from_error(error: &ResolverError) -> Self {
        Self::Error {
            protocol: PROTOCOL_VERSION,
            error: WireError {
                code: error.code(),
                message: error.message().to_owned(),
            },
        }
    }
}

/// Validates and executes a typed protocol request.
pub fn handle_request(request: &Request) -> Result<Response, ResolverError> {
    if request.protocol != PROTOCOL_VERSION {
        return Err(ResolverError::new(
            ErrorCode::InvalidRequest,
            format!("unsupported protocol version {}", request.protocol),
        ));
    }
    if request.target.is_empty() || request.roots.len() > MAX_ROOTS {
        return Err(ResolverError::new(
            ErrorCode::InvalidRequest,
            "target or roots are outside protocol bounds",
        ));
    }
    let bytes = fs::read(Path::new(&request.index_path)).map_err(|error| {
        ResolverError::new(ErrorCode::IndexIo, format!("cannot read index: {error}"))
    })?;
    let index = decode_index(&bytes)?;
    let packages = resolve(&index, &request.target, &request.roots)?;
    Ok(Response::Ok {
        protocol: PROTOCOL_VERSION,
        packages,
    })
}
