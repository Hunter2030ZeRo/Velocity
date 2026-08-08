use std::collections::BTreeMap;

use semver::Version;

use crate::{CompiledIndex, CompiledPackage, ErrorCode, ResolvedPackage, ResolverError};

use self::traversal::Traversal;

mod traversal;

struct Graph<'a> {
    packages: BTreeMap<u64, &'a CompiledPackage>,
    roots: BTreeMap<&'a str, u64>,
}

/// Resolves named roots for a target into deterministic dependency-first order.
pub fn resolve(
    index: &CompiledIndex,
    target: &str,
    roots: &[String],
) -> Result<Vec<ResolvedPackage>, ResolverError> {
    let graph = Graph::parse(index)?;
    let mut root_ids = roots
        .iter()
        .map(|root| {
            graph.roots.get(root.as_str()).copied().ok_or_else(|| {
                ResolverError::new(
                    ErrorCode::UnknownRoot,
                    format!("unknown package or alias '{root}'"),
                )
            })
        })
        .collect::<Result<Vec<_>, _>>()?;
    root_ids.sort_unstable();
    root_ids.dedup();
    let mut traversal = Traversal::new(&graph, target);
    for id in root_ids {
        traversal.walk(id)?;
    }
    let selected = traversal.into_selected();
    verify_conflicts(&selected)?;
    selected
        .into_iter()
        .map(|package| {
            let artifact = package
                .artifacts
                .iter()
                .find(|artifact| artifact.target == target)
                .cloned()
                .ok_or_else(|| {
                    ResolverError::new(
                        ErrorCode::MissingArtifact,
                        format!(
                            "package '{}' has no artifact for target '{target}'",
                            package.name
                        ),
                    )
                })?;
            Ok(ResolvedPackage {
                id: package.id,
                name: package.name.clone(),
                version: package.version.clone(),
                artifact,
            })
        })
        .collect()
}

impl<'a> Graph<'a> {
    fn parse(index: &'a CompiledIndex) -> Result<Self, ResolverError> {
        if index.format_version != 1 {
            return Err(ResolverError::new(
                ErrorCode::UnsupportedVersion,
                "compiled format version must be 1",
            ));
        }
        let mut packages = BTreeMap::new();
        let mut roots = BTreeMap::new();
        for package in &index.packages {
            Version::parse(&package.version).map_err(|error| {
                ResolverError::new(
                    ErrorCode::InvalidIndex,
                    format!("package '{}' has invalid version: {error}", package.name),
                )
            })?;
            if packages.insert(package.id, package).is_some()
                || roots.insert(package.name.as_str(), package.id).is_some()
            {
                return Err(ResolverError::new(
                    ErrorCode::InvalidIndex,
                    "duplicate package id or name",
                ));
            }
        }
        for alias in &index.aliases {
            if !packages.contains_key(&alias.package_id) {
                return Err(ResolverError::new(
                    ErrorCode::MissingReference,
                    format!(
                        "alias '{}' references missing package {}",
                        alias.alias, alias.package_id
                    ),
                ));
            }
            if roots
                .insert(alias.alias.as_str(), alias.package_id)
                .is_some()
            {
                return Err(ResolverError::new(
                    ErrorCode::InvalidIndex,
                    format!("duplicate package name or alias '{}'", alias.alias),
                ));
            }
        }
        Ok(Self { packages, roots })
    }
}

fn verify_conflicts(selected: &[&CompiledPackage]) -> Result<(), ResolverError> {
    let mut capability_counts = BTreeMap::new();
    for package in selected {
        for capability in std::iter::once(package.name.as_str())
            .chain(package.provides.iter().map(String::as_str))
        {
            capability_counts
                .entry(capability)
                .and_modify(|count: &mut usize| *count = count.saturating_add(1))
                .or_insert(1);
        }
    }
    for package in selected {
        let mut own_counts = BTreeMap::new();
        for capability in std::iter::once(package.name.as_str())
            .chain(package.provides.iter().map(String::as_str))
        {
            own_counts
                .entry(capability)
                .and_modify(|count: &mut usize| *count = count.saturating_add(1))
                .or_insert(1);
        }
        if let Some(conflict) = package.conflicts.iter().find(|conflict| {
            capability_counts
                .get(conflict.as_str())
                .copied()
                .unwrap_or(0)
                > own_counts.get(conflict.as_str()).copied().unwrap_or(0)
        }) {
            return Err(ResolverError::new(
                ErrorCode::Conflict,
                format!(
                    "package '{}' conflicts with selected capability '{conflict}'",
                    package.name
                ),
            ));
        }
    }
    Ok(())
}
