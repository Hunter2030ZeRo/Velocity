//! Stack-safety regression for dependency traversal.
#![allow(
    clippy::expect_used,
    reason = "test fixtures and failure assertions may panic"
)]

use std::process::Command;

use velocity_resolver::{
    Artifact, CompiledDependency, CompiledIndex, CompiledPackage, TargetPredicate, resolve,
};

const CHAIN_DEPTH: u64 = 16_384;
const TARGET: &str = "x86_64-unknown-linux-gnu";
const CHILD_ENV: &str = "VELOCITY_RESOLVER_DEEP_CHAIN_CHILD";

#[test]
fn very_deep_acyclic_chain_resolves_dependency_first_without_process_stack_growth() {
    // Given
    if std::env::var_os(CHILD_ENV).is_none() {
        let output = Command::new(std::env::current_exe().expect("test executable exists"))
            .arg("very_deep_acyclic_chain_resolves_dependency_first_without_process_stack_growth")
            .arg("--exact")
            .arg("--nocapture")
            .arg("--test-threads=1")
            .env(CHILD_ENV, "1")
            .env("RUST_MIN_STACK", "131072")
            .output()
            .expect("child test process starts");

        // When / Then
        assert!(
            output.status.success(),
            "deep-chain child failed: status={:?}\nstdout={}\nstderr={}",
            output.status.code(),
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        return;
    }
    let packages = (1..=CHAIN_DEPTH)
        .map(|id| CompiledPackage {
            id,
            name: format!("p{id}"),
            version: "1.0.0".into(),
            description: String::new(),
            homepage: None,
            license: None,
            provides: Vec::new(),
            conflicts: Vec::new(),
            dependencies: (id < CHAIN_DEPTH)
                .then(|| CompiledDependency {
                    package_id: id + 1,
                    requirement: "*".into(),
                    when: TargetPredicate {
                        targets: Vec::new(),
                    },
                })
                .into_iter()
                .collect(),
            artifacts: vec![Artifact {
                target: TARGET.into(),
                url: String::new(),
                sha256: String::new(),
                archive: String::new(),
                strip_components: 0,
                binaries: Vec::new(),
            }],
        })
        .collect();
    let index = CompiledIndex {
        format_version: 1,
        packages,
        aliases: Vec::new(),
    };

    // When
    let resolved = resolve(&index, TARGET, &["p1".into()]).expect("deep chain resolves");

    // Then
    assert_eq!(
        resolved.len(),
        usize::try_from(CHAIN_DEPTH).expect("depth fits usize")
    );
    assert_eq!(
        resolved.first().map(|package| package.id),
        Some(CHAIN_DEPTH)
    );
    assert_eq!(resolved.last().map(|package| package.id), Some(1));
    assert!(resolved.windows(2).all(|pair| {
        pair.first()
            .zip(pair.last())
            .is_some_and(|(left, right)| left.id == right.id + 1)
    }));
}
