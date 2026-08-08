use serde::Serialize;

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
/// Stable machine-readable resolver failure categories.
pub enum ErrorCode {
    /// The JSON request does not satisfy the supported protocol.
    InvalidRequest,
    /// The index file could not be read.
    IndexIo,
    /// The decoded index is malformed.
    InvalidIndex,
    /// The compressed or expanded index exceeds the byte bound.
    IndexTooLarge,
    /// An envelope, compiled-index, or protocol version is unsupported.
    UnsupportedVersion,
    /// A requested root is neither a canonical package name nor an alias.
    UnknownRoot,
    /// An active dependency or alias refers to an absent package id.
    MissingReference,
    /// A selected dependency contains invalid semver requirement syntax.
    InvalidRequirement,
    /// A selected dependency's version does not satisfy its requirement.
    VersionMismatch,
    /// The selected dependency graph contains a cycle.
    DependencyCycle,
    /// Two selected packages conflict by canonical name or provided capability.
    Conflict,
    /// A selected package has no artifact for the requested target.
    MissingArtifact,
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{message}")]
/// Typed resolver failure with a stable code and human-readable context.
pub struct ResolverError {
    code: ErrorCode,
    message: String,
}

impl ResolverError {
    /// Returns the stable wire error code.
    pub const fn code(&self) -> ErrorCode {
        self.code
    }

    /// Returns human-readable failure context.
    pub fn message(&self) -> &str {
        &self.message
    }

    /// Creates an invalid-request boundary error.
    pub fn invalid_request(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::InvalidRequest, message)
    }

    /// Creates an index I/O boundary error.
    pub fn index_io(message: impl Into<String>) -> Self {
        Self::new(ErrorCode::IndexIo, message)
    }

    pub(crate) fn new(code: ErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }
}
