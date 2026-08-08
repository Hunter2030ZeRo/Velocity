use std::io::Read;

use bincode::Options;

use crate::{CompiledIndex, ErrorCode, ResolverError};

mod bounds;
mod preflight;

const MAGIC: &[u8; 8] = b"VLTIDX1\0";
const ENVELOPE_BYTES: usize = 12;
/// Maximum accepted compressed or decompressed index size.
pub const MAX_INDEX_BYTES: usize = 64 * 1024 * 1024;

/// Decompresses and decodes a registry-compatible `velocity.idx.zst` byte slice.
pub fn decode_index(compressed: &[u8]) -> Result<CompiledIndex, ResolverError> {
    if compressed.len() > MAX_INDEX_BYTES {
        return Err(ResolverError::new(
            ErrorCode::IndexTooLarge,
            "compressed index exceeds 64 MiB",
        ));
    }
    let decoder = zstd::stream::read::Decoder::new(compressed).map_err(|error| {
        ResolverError::new(
            ErrorCode::InvalidIndex,
            format!("invalid zstd stream: {error}"),
        )
    })?;
    let limit = u64::try_from(MAX_INDEX_BYTES)
        .map_err(|error| ResolverError::new(ErrorCode::InvalidIndex, error.to_string()))?;
    let mut bytes = Vec::new();
    decoder
        .take(limit + 1)
        .read_to_end(&mut bytes)
        .map_err(|error| {
            ResolverError::new(
                ErrorCode::InvalidIndex,
                format!("cannot decompress index: {error}"),
            )
        })?;
    if bytes.len() > MAX_INDEX_BYTES {
        return Err(ResolverError::new(
            ErrorCode::IndexTooLarge,
            "decompressed index exceeds 64 MiB",
        ));
    }
    decode_payload(&bytes)
}

fn decode_payload(bytes: &[u8]) -> Result<CompiledIndex, ResolverError> {
    let header = bytes
        .get(..ENVELOPE_BYTES)
        .ok_or_else(|| ResolverError::new(ErrorCode::InvalidIndex, "index header is truncated"))?;
    if header.get(..MAGIC.len()) != Some(MAGIC.as_slice()) {
        return Err(ResolverError::new(
            ErrorCode::InvalidIndex,
            "index magic is invalid",
        ));
    }
    let version_bytes: [u8; 4] = header
        .get(MAGIC.len()..ENVELOPE_BYTES)
        .and_then(|value| value.try_into().ok())
        .ok_or_else(|| ResolverError::new(ErrorCode::InvalidIndex, "index version is truncated"))?;
    let version = u32::from_le_bytes(version_bytes);
    if version != 1 {
        return Err(ResolverError::new(
            ErrorCode::UnsupportedVersion,
            format!("unsupported index envelope version {version}"),
        ));
    }
    let payload = bytes
        .get(ENVELOPE_BYTES..)
        .ok_or_else(|| ResolverError::new(ErrorCode::InvalidIndex, "index payload is missing"))?;
    preflight::validate(payload)?;
    let limit = u64::try_from(MAX_INDEX_BYTES)
        .map_err(|error| ResolverError::new(ErrorCode::InvalidIndex, error.to_string()))?;
    let index: CompiledIndex = bincode::DefaultOptions::new()
        .with_fixint_encoding()
        .with_limit(limit)
        .reject_trailing_bytes()
        .deserialize(payload)
        .map_err(|error| {
            ResolverError::new(
                ErrorCode::InvalidIndex,
                format!("invalid index payload: {error}"),
            )
        })?;
    if index.format_version != 1 {
        return Err(ResolverError::new(
            ErrorCode::UnsupportedVersion,
            format!(
                "unsupported compiled format version {}",
                index.format_version
            ),
        ));
    }
    bounds::validate(&index)?;
    Ok(index)
}
