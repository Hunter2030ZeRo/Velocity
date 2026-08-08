use velocity_resolver::{
    Artifact, BinaryMapping, CompiledDependency, CompiledPackage, TargetPredicate,
};

pub(crate) struct PackageFixture<'a> {
    pub(crate) id: u64,
    pub(crate) name: &'a str,
    pub(crate) version: &'a str,
    pub(crate) dependencies: Vec<CompiledDependency>,
}

pub(crate) fn package(fixture: PackageFixture<'_>) -> CompiledPackage {
    CompiledPackage {
        id: fixture.id,
        name: fixture.name.into(),
        version: fixture.version.into(),
        description: String::new(),
        homepage: None,
        license: None,
        provides: Vec::new(),
        conflicts: Vec::new(),
        dependencies: fixture.dependencies,
        artifacts: vec![artifact("x86_64-unknown-linux-gnu")],
    }
}

pub(crate) fn dependency(id: u64, requirement: &str, targets: &[&str]) -> CompiledDependency {
    CompiledDependency {
        package_id: id,
        requirement: requirement.into(),
        when: TargetPredicate {
            targets: targets.iter().map(|value| (*value).into()).collect(),
        },
    }
}

fn artifact(target: &str) -> Artifact {
    Artifact {
        target: target.into(),
        url: "https://example.invalid/tool.tar.zst".into(),
        sha256: "00".repeat(32),
        archive: "tar.zst".into(),
        strip_components: 1,
        binaries: vec![BinaryMapping {
            source: "tool".into(),
            name: "tool".into(),
        }],
    }
}
