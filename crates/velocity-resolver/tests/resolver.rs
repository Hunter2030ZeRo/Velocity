//! Dependency selection, validation, ordering, and property tests.
#![allow(
    clippy::expect_used,
    reason = "test fixtures and failure assertions may panic"
)]

use proptest::prelude::*;
/// Shared resolver test fixtures.
pub mod support;

use support::{PackageFixture, dependency, package};
use velocity_resolver::{CompiledAlias, CompiledIndex, ErrorCode, resolve};

#[test]
fn dependencies_precede_roots_when_alias_and_target_dependency_are_resolved() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![
            package(PackageFixture {
                id: 2,
                name: "app",
                version: "2.0.0",
                dependencies: vec![dependency(1, "^1.0", &["x86_64-unknown-linux-gnu"])],
            }),
            package(PackageFixture {
                id: 1,
                name: "lib",
                version: "1.4.0",
                dependencies: Vec::new(),
            }),
        ],
        aliases: vec![CompiledAlias {
            alias: "application".into(),
            package_id: 2,
        }],
    };

    // When
    let result = resolve(&index, "x86_64-unknown-linux-gnu", &["application".into()])
        .expect("valid graph resolves");

    // Then
    assert_eq!(
        result
            .iter()
            .map(|item| item.name.as_str())
            .collect::<Vec<_>>(),
        ["lib", "app"]
    );
}

#[test]
fn inactive_target_dependency_is_ignored() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![package(PackageFixture {
            id: 2,
            name: "app",
            version: "2.0.0",
            dependencies: vec![dependency(99, "*", &["aarch64-apple-darwin"])],
        })],
        aliases: Vec::new(),
    };

    // When
    let result = resolve(&index, "x86_64-unknown-linux-gnu", &["app".into()]);

    // Then
    assert_eq!(
        result
            .expect("inactive dependency does not participate")
            .len(),
        1
    );
}

#[test]
fn cycle_is_rejected() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![
            package(PackageFixture {
                id: 1,
                name: "a",
                version: "1.0.0",
                dependencies: vec![dependency(2, "*", &[])],
            }),
            package(PackageFixture {
                id: 2,
                name: "b",
                version: "1.0.0",
                dependencies: vec![dependency(1, "*", &[])],
            }),
        ],
        aliases: Vec::new(),
    };

    // When
    let error =
        resolve(&index, "x86_64-unknown-linux-gnu", &["a".into()]).expect_err("cycle must fail");

    // Then
    assert_eq!(error.code(), ErrorCode::DependencyCycle);
}

#[test]
fn shared_dependencies_are_selected_once_in_deterministic_postorder() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![
            package(PackageFixture {
                id: 1,
                name: "root",
                version: "1.0.0",
                dependencies: vec![dependency(3, "*", &[]), dependency(2, "*", &[])],
            }),
            package(PackageFixture {
                id: 2,
                name: "left",
                version: "1.0.0",
                dependencies: vec![dependency(4, "*", &[])],
            }),
            package(PackageFixture {
                id: 3,
                name: "right",
                version: "1.0.0",
                dependencies: vec![dependency(4, "*", &[])],
            }),
            package(PackageFixture {
                id: 4,
                name: "shared",
                version: "1.0.0",
                dependencies: Vec::new(),
            }),
        ],
        aliases: Vec::new(),
    };

    // When
    let resolved = resolve(&index, "x86_64-unknown-linux-gnu", &["root".into()])
        .expect("shared graph resolves");

    // Then
    assert_eq!(
        resolved
            .iter()
            .map(|package| package.id)
            .collect::<Vec<_>>(),
        [4, 2, 3, 1]
    );
}

#[test]
fn active_invalid_requirement_precedes_cycle_detection() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![package(PackageFixture {
            id: 1,
            name: "cycle",
            version: "1.0.0",
            dependencies: vec![dependency(1, "invalid req", &[])],
        })],
        aliases: Vec::new(),
    };

    // When
    let error = resolve(&index, "x86_64-unknown-linux-gnu", &["cycle".into()])
        .expect_err("invalid requirement fails before self-cycle detection");

    // Then
    assert_eq!(error.code(), ErrorCode::InvalidRequirement);
    assert!(error.message().contains("invalid req") && error.message().contains("cycle"));
}

#[test]
fn active_version_mismatch_precedes_cycle_detection() {
    // Given
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![package(PackageFixture {
            id: 1,
            name: "cycle",
            version: "1.0.0",
            dependencies: vec![dependency(1, "^2", &[])],
        })],
        aliases: Vec::new(),
    };

    // When
    let error = resolve(&index, "x86_64-unknown-linux-gnu", &["cycle".into()])
        .expect_err("version mismatch fails before self-cycle detection");

    // Then
    assert_eq!(error.code(), ErrorCode::VersionMismatch);
    assert!(error.message().contains("cycle") && error.message().contains("^2"));
}

proptest! {
    #[test]
    fn independent_roots_have_deterministic_id_order(ids in prop::collection::btree_set(1_u64..10_000, 1..20)) {
        // Given
        let packages = ids
            .iter()
            .map(|id| {
                package(PackageFixture {
                    id: *id,
                    name: &format!("p{id}"),
                    version: "1.0.0",
                    dependencies: Vec::new(),
                })
            })
            .collect::<Vec<_>>();
        let roots = packages.iter().rev().map(|item| item.name.clone()).collect::<Vec<_>>();
        let index = CompiledIndex { format_version: 1, packages, aliases: Vec::new() };

        // When
        let resolved = resolve(&index, "x86_64-unknown-linux-gnu", &roots).expect("valid roots resolve");
        // Then
        prop_assert!(resolved.windows(2).all(|pair| pair.first().zip(pair.last()).is_some_and(|(left, right)| left.id < right.id)));
    }
}
