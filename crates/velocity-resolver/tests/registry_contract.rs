//! Compatibility proof against the generated registry index artifact.
#![allow(
    clippy::expect_used,
    reason = "fixture decoding uses explicit test failure messages"
)]

use velocity_resolver::{decode_index, resolve};

const FIXTURE_BYTES: usize = 1_616;

fn registry_fixture() -> Vec<u8> {
    let hex = include_str!("fixtures/velocity-registry.idx.zst.hex")
        .split_whitespace()
        .collect::<String>();
    assert_eq!(hex.len() % 2, 0, "fixture hex has odd length");
    hex.as_bytes()
        .chunks_exact(2)
        .map(|pair| {
            let digits = std::str::from_utf8(pair).expect("fixture hex is UTF-8");
            u8::from_str_radix(digits, 16).expect("fixture contains hexadecimal bytes")
        })
        .collect()
}

#[test]
fn generated_registry_contract_resolves_fd_for_linux() {
    // Given
    let compressed = registry_fixture();
    assert_eq!(compressed.len(), FIXTURE_BYTES);

    // When
    let index = decode_index(&compressed).expect("generated registry index decodes");
    let resolved = resolve(&index, "x86_64-unknown-linux-gnu", &[String::from("fd")])
        .expect("generated fd root resolves");

    // Then
    assert_eq!(resolved.len(), 1);
    let package = resolved.first().expect("one fd package resolves");
    assert_eq!(package.name, "fd");
    assert_eq!(package.version, "10.4.2");
    assert_eq!(package.artifact.target, "x86_64-unknown-linux-gnu");
    assert_eq!(
        package.artifact.url,
        "https://github.com/sharkdp/fd/releases/download/v10.4.2/fd-v10.4.2-x86_64-unknown-linux-gnu.tar.gz"
    );
    assert_eq!(
        package.artifact.sha256,
        "def59805cd14b5651b68990855f426ad087f3b96881296d963910431ba3143c8"
    );
    assert_eq!(package.artifact.archive, "tar.gz");
    assert_eq!(package.artifact.strip_components, 1);
    assert_eq!(package.artifact.binaries.len(), 1);
    let binary = package
        .artifact
        .binaries
        .first()
        .expect("one fd binary mapping resolves");
    assert_eq!(binary.source, "fd");
    assert_eq!(binary.name, "fd");
}
