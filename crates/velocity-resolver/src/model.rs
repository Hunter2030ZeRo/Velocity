use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// Registry's complete compiled package index.
pub struct CompiledIndex {
    /// Compiled payload format version.
    pub format_version: u32,
    /// Canonical package records.
    pub packages: Vec<CompiledPackage>,
    /// Alternate root names.
    pub aliases: Vec<CompiledAlias>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// One compiled package record.
pub struct CompiledPackage {
    /// Stable package identifier.
    pub id: u64,
    /// Canonical package name.
    pub name: String,
    /// Semantic version.
    pub version: String,
    /// Human-readable summary.
    pub description: String,
    /// Optional project homepage.
    pub homepage: Option<String>,
    /// Optional license expression.
    pub license: Option<String>,
    /// Capability names supplied by this package.
    pub provides: Vec<String>,
    /// Canonical or capability names incompatible with this package.
    pub conflicts: Vec<String>,
    /// Package requirements.
    pub dependencies: Vec<CompiledDependency>,
    /// Target-specific downloadable artifacts.
    pub artifacts: Vec<Artifact>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// One dependency edge in the compiled graph.
pub struct CompiledDependency {
    /// Required package identifier.
    pub package_id: u64,
    /// Semantic-version requirement.
    pub requirement: String,
    /// Targets on which the edge is active.
    pub when: TargetPredicate,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// Target selector for a dependency edge.
pub struct TargetPredicate {
    /// Active targets, or empty for every target.
    pub targets: Vec<String>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// Alternate package root name.
pub struct CompiledAlias {
    /// Alternate name.
    pub alias: String,
    /// Canonical package identifier.
    pub package_id: u64,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// Downloadable package artifact for one target.
pub struct Artifact {
    /// Target triple.
    pub target: String,
    /// Download URL.
    pub url: String,
    /// Expected hexadecimal SHA-256 digest.
    pub sha256: String,
    /// Archive format name.
    pub archive: String,
    /// Leading archive path components to remove.
    pub strip_components: u32,
    /// Executables exposed from the extracted artifact.
    pub binaries: Vec<BinaryMapping>,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// Extracted binary source and installed name.
pub struct BinaryMapping {
    /// Source path inside the extracted artifact.
    pub source: String,
    /// Installed executable name.
    pub name: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
/// Selected package returned on the JSON wire.
pub struct ResolvedPackage {
    /// Stable package identifier.
    pub id: u64,
    /// Canonical package name.
    pub name: String,
    /// Selected semantic version.
    pub version: String,
    /// Artifact matching the request target.
    pub artifact: Artifact,
}
