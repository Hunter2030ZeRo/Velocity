//! Decoder, deterministic dependency resolver, and JSON sidecar protocol for Velocity indexes.

mod error;
mod index;
mod model;
mod protocol;
mod resolver;

pub use error::{ErrorCode, ResolverError};
pub use index::{MAX_INDEX_BYTES, decode_index};
pub use model::{
    Artifact, BinaryMapping, CompiledAlias, CompiledDependency, CompiledIndex, CompiledPackage,
    ResolvedPackage, TargetPredicate,
};
pub use protocol::{PROTOCOL_VERSION, Request, Response, WireError, handle_request};
pub use resolver::resolve;
