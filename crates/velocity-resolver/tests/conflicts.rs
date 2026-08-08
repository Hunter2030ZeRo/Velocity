//! Conflict detection integration tests.
#![allow(
    clippy::expect_used,
    reason = "test fixtures and failure assertions may panic"
)]

/// Shared resolver test fixtures.
pub mod support;

use support::{PackageFixture, dependency, package};
use velocity_resolver::{CompiledIndex, ErrorCode, resolve};

#[test]
fn selected_provide_conflict_is_rejected() {
    // Given
    let mut app = package(PackageFixture {
        id: 1,
        name: "app",
        version: "1.0.0",
        dependencies: vec![dependency(2, "*", &[])],
    });
    app.conflicts.push("ssl".into());
    let mut tls = package(PackageFixture {
        id: 2,
        name: "tls",
        version: "1.0.0",
        dependencies: Vec::new(),
    });
    tls.provides.push("ssl".into());
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![app, tls],
        aliases: Vec::new(),
    };

    // When
    let error = resolve(&index, "x86_64-unknown-linux-gnu", &["app".into()])
        .expect_err("selected capability conflict must fail");

    // Then
    assert_eq!(error.code(), ErrorCode::Conflict);
}

#[test]
fn conflict_error_preserves_selected_package_and_declared_conflict_order() {
    // Given
    let mut first = package(PackageFixture {
        id: 1,
        name: "first",
        version: "1.0.0",
        dependencies: Vec::new(),
    });
    first.conflicts = vec!["shared".into(), "second".into()];
    let mut second = package(PackageFixture {
        id: 2,
        name: "second",
        version: "1.0.0",
        dependencies: Vec::new(),
    });
    second.provides.push("shared".into());
    let root = package(PackageFixture {
        id: 3,
        name: "root",
        version: "1.0.0",
        dependencies: vec![dependency(2, "*", &[]), dependency(1, "*", &[])],
    });
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![root, second, first],
        aliases: Vec::new(),
    };

    // When
    let error = resolve(&index, "x86_64-unknown-linux-gnu", &["root".into()])
        .expect_err("first selected package conflict must win");

    // Then
    assert_eq!(error.code(), ErrorCode::Conflict);
    assert_eq!(
        error.message(),
        "package 'first' conflicts with selected capability 'shared'"
    );
}

#[test]
fn canonical_package_name_is_a_conflict_capability() {
    // Given
    let mut root = package(PackageFixture {
        id: 1,
        name: "root",
        version: "1.0.0",
        dependencies: vec![dependency(2, "*", &[])],
    });
    root.conflicts.push("second".into());
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![
            root,
            package(PackageFixture {
                id: 2,
                name: "second",
                version: "1.0.0",
                dependencies: Vec::new(),
            }),
        ],
        aliases: Vec::new(),
    };

    // When
    let error = resolve(&index, "x86_64-unknown-linux-gnu", &["root".into()])
        .expect_err("canonical name must conflict");

    // Then
    assert_eq!(
        error.message(),
        "package 'root' conflicts with selected capability 'second'"
    );
}

#[test]
fn self_and_duplicate_capabilities_do_not_conflict() {
    // Given
    let mut solo = package(PackageFixture {
        id: 1,
        name: "solo",
        version: "1.0.0",
        dependencies: Vec::new(),
    });
    solo.provides = vec!["solo".into(), "cap".into(), "cap".into()];
    solo.conflicts = vec!["solo".into(), "cap".into()];
    let index = CompiledIndex {
        format_version: 1,
        packages: vec![solo],
        aliases: Vec::new(),
    };

    // When
    let resolved = resolve(&index, "x86_64-unknown-linux-gnu", &["solo".into()]);

    // Then
    assert_eq!(resolved.expect("self capabilities are excluded").len(), 1);
}
