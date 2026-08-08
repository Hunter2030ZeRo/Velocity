use crate::{CompiledIndex, CompiledPackage, ErrorCode, ResolverError};

pub(super) const MAX_PACKAGES: usize = 20_000;
pub(super) const MAX_ALIASES: usize = 20_000;
pub(super) const MAX_DEPENDENCIES_PER_PACKAGE: usize = 1_024;
pub(super) const MAX_ARTIFACTS_PER_PACKAGE: usize = 32;
pub(super) const MAX_BINARIES_PER_ARTIFACT: usize = 64;
pub(super) const MAX_CAPABILITIES_PER_PACKAGE: usize = 256;
pub(super) const MAX_CONFLICTS_PER_PACKAGE: usize = 256;
pub(super) const MAX_TARGETS_PER_PREDICATE: usize = 64;
pub(super) const MAX_STRING_BYTES: usize = 16 * 1024;

#[derive(Clone, Copy)]
pub(super) struct AggregateLimit {
    pub(super) maximum: usize,
    pub(super) label: &'static str,
}

impl AggregateLimit {
    pub(super) const fn new(maximum: usize, label: &'static str) -> Self {
        Self { maximum, label }
    }
}

pub(super) const DEPENDENCIES_TOTAL: AggregateLimit =
    AggregateLimit::new(20_000, "dependency count");
pub(super) const ARTIFACTS_TOTAL: AggregateLimit = AggregateLimit::new(20_000, "artifact count");
pub(super) const BINARIES_TOTAL: AggregateLimit = AggregateLimit::new(20_000, "binary count");
pub(super) const CAPABILITIES_TOTAL: AggregateLimit =
    AggregateLimit::new(20_000, "capability count");
pub(super) const CONFLICTS_TOTAL: AggregateLimit = AggregateLimit::new(20_000, "conflict count");
pub(super) const TARGETS_TOTAL: AggregateLimit =
    AggregateLimit::new(20_000, "predicate target count");

#[derive(Default)]
struct Totals {
    dependencies: usize,
    artifacts: usize,
    binaries: usize,
    capabilities: usize,
    conflicts: usize,
    targets: usize,
}

pub(super) fn validate(index: &CompiledIndex) -> Result<(), ResolverError> {
    check_len(index.packages.len(), MAX_PACKAGES, "package count")?;
    check_len(index.aliases.len(), MAX_ALIASES, "alias count")?;

    let mut totals = Totals::default();
    for package in &index.packages {
        validate_package(package, &mut totals)?;
    }

    for alias in &index.aliases {
        check_text(&alias.alias, "alias")?;
    }
    Ok(())
}

fn validate_package(package: &CompiledPackage, totals: &mut Totals) -> Result<(), ResolverError> {
    check_text(&package.name, "package name")?;
    check_text(&package.version, "package version")?;
    check_text(&package.description, "package description")?;
    check_optional_text(package.homepage.as_deref(), "package homepage")?;
    check_optional_text(package.license.as_deref(), "package license")?;

    check_len(
        package.provides.len(),
        MAX_CAPABILITIES_PER_PACKAGE,
        "capabilities per package",
    )?;
    add_total(
        &mut totals.capabilities,
        package.provides.len(),
        CAPABILITIES_TOTAL,
    )?;
    for capability in &package.provides {
        check_text(capability, "capability")?;
    }

    check_len(
        package.conflicts.len(),
        MAX_CONFLICTS_PER_PACKAGE,
        "conflicts per package",
    )?;
    add_total(
        &mut totals.conflicts,
        package.conflicts.len(),
        CONFLICTS_TOTAL,
    )?;
    for conflict in &package.conflicts {
        check_text(conflict, "conflict")?;
    }

    check_len(
        package.dependencies.len(),
        MAX_DEPENDENCIES_PER_PACKAGE,
        "dependencies per package",
    )?;
    add_total(
        &mut totals.dependencies,
        package.dependencies.len(),
        DEPENDENCIES_TOTAL,
    )?;
    for dependency in &package.dependencies {
        check_text(&dependency.requirement, "dependency requirement")?;
        check_len(
            dependency.when.targets.len(),
            MAX_TARGETS_PER_PREDICATE,
            "targets per predicate",
        )?;
        add_total(
            &mut totals.targets,
            dependency.when.targets.len(),
            TARGETS_TOTAL,
        )?;
        for target in &dependency.when.targets {
            check_text(target, "predicate target")?;
        }
    }

    check_len(
        package.artifacts.len(),
        MAX_ARTIFACTS_PER_PACKAGE,
        "artifacts per package",
    )?;
    add_total(
        &mut totals.artifacts,
        package.artifacts.len(),
        ARTIFACTS_TOTAL,
    )?;
    for artifact in &package.artifacts {
        check_text(&artifact.target, "artifact target")?;
        check_text(&artifact.url, "artifact URL")?;
        check_text(&artifact.sha256, "artifact SHA-256")?;
        check_text(&artifact.archive, "artifact archive")?;
        check_len(
            artifact.binaries.len(),
            MAX_BINARIES_PER_ARTIFACT,
            "binaries per artifact",
        )?;
        add_total(
            &mut totals.binaries,
            artifact.binaries.len(),
            BINARIES_TOTAL,
        )?;
        for binary in &artifact.binaries {
            check_text(&binary.source, "binary source")?;
            check_text(&binary.name, "binary name")?;
        }
    }
    Ok(())
}

fn check_len(length: usize, maximum: usize, label: &str) -> Result<(), ResolverError> {
    if length > maximum {
        return Err(resource_limit(label));
    }
    Ok(())
}

fn add_total(total: &mut usize, count: usize, limit: AggregateLimit) -> Result<(), ResolverError> {
    *total = total
        .checked_add(count)
        .ok_or_else(|| resource_limit(limit.label))?;
    check_len(*total, limit.maximum, limit.label)
}

fn check_text(value: &str, label: &str) -> Result<(), ResolverError> {
    check_len(value.len(), MAX_STRING_BYTES, label)
}

fn check_optional_text(value: Option<&str>, label: &str) -> Result<(), ResolverError> {
    value.map_or(Ok(()), |value| check_text(value, label))
}

pub(super) fn resource_limit(label: &str) -> ResolverError {
    ResolverError::new(
        ErrorCode::InvalidIndex,
        format!("index resource limit exceeded: {label}"),
    )
}
