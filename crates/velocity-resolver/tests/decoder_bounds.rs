//! Resource-cardinality and allocation-bound tests for the binary index decoder.
#![allow(
    clippy::expect_used,
    reason = "test fixtures and failure assertions may panic"
)]

use bincode::Options;
use velocity_resolver::{
    Artifact, BinaryMapping, CompiledAlias, CompiledDependency, CompiledIndex, CompiledPackage,
    ErrorCode, TargetPredicate, decode_index,
};

fn encoded_index(index: &CompiledIndex) -> Vec<u8> {
    let payload = bincode::DefaultOptions::new()
        .with_fixint_encoding()
        .allow_trailing_bytes()
        .serialize(index)
        .expect("fixture serializes");
    encoded_payload(&payload)
}

fn encoded_payload(payload: &[u8]) -> Vec<u8> {
    let mut raw = b"VLTIDX1\0".to_vec();
    raw.extend_from_slice(&1_u32.to_le_bytes());
    raw.extend_from_slice(payload);
    zstd::stream::encode_all(raw.as_slice(), 1).expect("fixture compresses")
}

fn empty_package(id: u64) -> CompiledPackage {
    CompiledPackage {
        id,
        name: format!("package-{id}"),
        version: "1.0.0".into(),
        description: String::new(),
        homepage: None,
        license: None,
        provides: Vec::new(),
        conflicts: Vec::new(),
        dependencies: Vec::new(),
        artifacts: Vec::new(),
    }
}

fn index_with(package: CompiledPackage) -> CompiledIndex {
    CompiledIndex {
        format_version: 1,
        packages: vec![package],
        aliases: Vec::new(),
    }
}

fn rejection(index: &CompiledIndex) -> velocity_resolver::ResolverError {
    decode_index(&encoded_index(index)).expect_err("index resource limit is rejected")
}

#[test]
fn index_exceeding_package_bound_is_rejected() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: (1_u64..=20_001).map(empty_package).collect(),
        aliases: Vec::new(),
    };

    // When
    let error = rejection(&index);

    // Then
    assert_eq!(error.code(), ErrorCode::InvalidIndex);
}

#[test]
fn declared_pathological_package_count_is_rejected_before_bincode_allocates() {
    // Given
    let mut payload = 1_u32.to_le_bytes().to_vec();
    payload.extend_from_slice(&u64::MAX.to_le_bytes());
    let bytes = encoded_payload(&payload);

    // When
    let error = decode_index(&bytes).expect_err("pathological package count is rejected");

    // Then
    assert_eq!(error.code(), ErrorCode::InvalidIndex);
}

#[test]
fn index_exceeding_alias_bound_is_rejected() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: Vec::new(),
        aliases: (0_u64..20_001)
            .map(|package_id| CompiledAlias {
                alias: format!("alias-{package_id}"),
                package_id,
            })
            .collect(),
    };

    // When
    let error = rejection(&index);

    // Then
    assert_eq!(error.code(), ErrorCode::InvalidIndex);
}

#[test]
fn index_exceeding_per_record_resource_bounds_is_rejected() {
    // Given
    let mut capabilities = empty_package(1);
    capabilities.provides = vec![String::new(); 257];
    let mut conflicts = empty_package(1);
    conflicts.conflicts = vec![String::new(); 257];
    let mut dependencies = empty_package(1);
    dependencies.dependencies = vec![dependency(Vec::new()); 1_025];
    let mut targets = empty_package(1);
    targets.dependencies = vec![dependency(vec![String::new(); 65])];
    let mut artifacts = empty_package(1);
    artifacts.artifacts = vec![artifact(Vec::new()); 33];
    let mut binaries = empty_package(1);
    binaries.artifacts = vec![artifact(vec![binary(); 65])];
    let mut long_string = empty_package(1);
    long_string.name = "x".repeat(16 * 1024 + 1);

    // When
    let errors = [
        rejection(&index_with(capabilities)),
        rejection(&index_with(conflicts)),
        rejection(&index_with(dependencies)),
        rejection(&index_with(targets)),
        rejection(&index_with(artifacts)),
        rejection(&index_with(binaries)),
        rejection(&index_with(long_string)),
    ];

    // Then
    assert!(
        errors
            .iter()
            .all(|error| error.code() == ErrorCode::InvalidIndex)
    );
}

#[test]
fn index_exceeding_aggregate_resource_bounds_is_rejected() {
    // Given
    let mut capabilities = empty_package(1);
    capabilities.provides = vec![String::new(); 256];
    let mut conflicts = empty_package(1);
    conflicts.conflicts = vec![String::new(); 256];
    let mut dependencies = empty_package(1);
    dependencies.dependencies = vec![dependency(Vec::new()); 1_024];
    let mut targets = empty_package(1);
    targets.dependencies = vec![dependency(vec![String::new(); 64]); 16];
    let mut artifacts = empty_package(1);
    artifacts.artifacts = vec![artifact(Vec::new()); 32];
    let mut binaries = empty_package(1);
    binaries.artifacts = vec![artifact(vec![binary(); 64]); 32];
    let indexes = [
        repeated_packages(capabilities, 79),
        repeated_packages(conflicts, 79),
        repeated_packages(dependencies, 20),
        repeated_packages(targets, 20),
        repeated_packages(artifacts, 626),
        repeated_packages(binaries, 10),
    ];

    // When
    let errors = indexes.iter().map(rejection).collect::<Vec<_>>();

    // Then
    assert!(
        errors
            .iter()
            .all(|error| error.code() == ErrorCode::InvalidIndex)
    );
}

const fn dependency(targets: Vec<String>) -> CompiledDependency {
    CompiledDependency {
        package_id: 1,
        requirement: String::new(),
        when: TargetPredicate { targets },
    }
}

const fn artifact(binaries: Vec<BinaryMapping>) -> Artifact {
    Artifact {
        target: String::new(),
        url: String::new(),
        sha256: String::new(),
        archive: String::new(),
        strip_components: 0,
        binaries,
    }
}

const fn binary() -> BinaryMapping {
    BinaryMapping {
        source: String::new(),
        name: String::new(),
    }
}

fn repeated_packages(package: CompiledPackage, count: usize) -> CompiledIndex {
    CompiledIndex {
        format_version: 1,
        packages: vec![package; count],
        aliases: Vec::new(),
    }
}
