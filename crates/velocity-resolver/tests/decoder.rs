//! Binary index compatibility tests.
#![allow(
    clippy::expect_used,
    reason = "test fixtures and failure assertions may panic"
)]

use bincode::Options;
use velocity_resolver::{CompiledIndex, ErrorCode, decode_index};

fn encoded_index(version: u32, index: &CompiledIndex) -> Vec<u8> {
    let payload = bincode::DefaultOptions::new()
        .with_fixint_encoding()
        .allow_trailing_bytes()
        .serialize(index)
        .expect("fixture serializes");
    let mut raw = b"VLTIDX1\0".to_vec();
    raw.extend_from_slice(&version.to_le_bytes());
    raw.extend_from_slice(&payload);
    zstd::stream::encode_all(raw.as_slice(), 1).expect("fixture compresses")
}

#[test]
fn compatible_registry_index_decodes() {
    // Given
    let expected = CompiledIndex {
        format_version: 1,
        packages: Vec::new(),
        aliases: Vec::new(),
    };
    let bytes = encoded_index(1, &expected);

    // When
    let decoded = decode_index(&bytes).expect("compatible index decodes");

    // Then
    assert_eq!(decoded, expected);
}

#[test]
fn unsupported_envelope_version_is_rejected() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: Vec::new(),
        aliases: Vec::new(),
    };
    let bytes = encoded_index(2, &index);

    // When
    let error = decode_index(&bytes).expect_err("version two is unsupported");

    // Then
    assert_eq!(error.code(), ErrorCode::UnsupportedVersion);
}

#[test]
fn oversized_decompressed_index_is_rejected() {
    // Given
    let raw = vec![0_u8; velocity_resolver::MAX_INDEX_BYTES + 1];
    let bytes = zstd::stream::encode_all(raw.as_slice(), 1).expect("fixture compresses");

    // When
    let error = decode_index(&bytes).expect_err("oversized index is rejected");

    // Then
    assert_eq!(error.code(), ErrorCode::IndexTooLarge);
}
