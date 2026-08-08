use std::collections::BTreeMap;

use semver::{Version, VersionReq};

use crate::{CompiledDependency, CompiledPackage, ErrorCode, ResolverError};

use super::Graph;

#[derive(Clone, Copy, Eq, PartialEq)]
enum VisitState {
    Visiting,
    Complete,
}

struct Frame<'index> {
    package: &'index CompiledPackage,
    dependencies: Vec<&'index CompiledDependency>,
    next_dependency: usize,
}

#[derive(Clone, Copy)]
struct Edge<'index> {
    package: &'index CompiledPackage,
    dependency: &'index CompiledDependency,
}

pub(super) struct Traversal<'index, 'target> {
    graph: &'index Graph<'index>,
    target: &'target str,
    states: BTreeMap<u64, VisitState>,
    selected: Vec<&'index CompiledPackage>,
}

impl<'index> Frame<'index> {
    fn new(package: &'index CompiledPackage, target: &str) -> Self {
        let mut dependencies = package
            .dependencies
            .iter()
            .filter(|dependency| {
                dependency.when.targets.is_empty()
                    || dependency.when.targets.iter().any(|value| value == target)
            })
            .collect::<Vec<_>>();
        dependencies.sort_by_key(|dependency| dependency.package_id);
        Self {
            package,
            dependencies,
            next_dependency: 0,
        }
    }

    fn advance(&mut self) -> Option<&'index CompiledDependency> {
        let dependency = self.dependencies.get(self.next_dependency).copied();
        if dependency.is_some() {
            self.next_dependency += 1;
        }
        dependency
    }
}

impl<'index, 'target> Traversal<'index, 'target> {
    pub(super) const fn new(graph: &'index Graph<'index>, target: &'target str) -> Self {
        Self {
            graph,
            target,
            states: BTreeMap::new(),
            selected: Vec::new(),
        }
    }

    pub(super) fn walk(&mut self, id: u64) -> Result<(), ResolverError> {
        match self.states.get(&id) {
            Some(VisitState::Complete) => return Ok(()),
            Some(VisitState::Visiting) => return Err(cycle_error(id)),
            None => {}
        }
        let package = self.package(id)?;
        self.states.insert(id, VisitState::Visiting);
        let mut frames = vec![Frame::new(package, self.target)];
        while let Some(frame) = frames.last_mut() {
            if let Some(dependency) = frame.advance() {
                let required = self.validate_edge(Edge {
                    package: frame.package,
                    dependency,
                })?;
                match self.states.get(&dependency.package_id) {
                    Some(VisitState::Complete) => {}
                    Some(VisitState::Visiting) => {
                        return Err(cycle_error(dependency.package_id));
                    }
                    None => {
                        self.states
                            .insert(dependency.package_id, VisitState::Visiting);
                        frames.push(Frame::new(required, self.target));
                    }
                }
            } else if let Some(completed) = frames.pop() {
                self.states
                    .insert(completed.package.id, VisitState::Complete);
                self.selected.push(completed.package);
            }
        }
        Ok(())
    }

    pub(super) fn into_selected(self) -> Vec<&'index CompiledPackage> {
        self.selected
    }

    fn package(&self, id: u64) -> Result<&'index CompiledPackage, ResolverError> {
        self.graph.packages.get(&id).copied().ok_or_else(|| {
            ResolverError::new(
                ErrorCode::MissingReference,
                format!("missing package id {id}"),
            )
        })
    }

    fn validate_edge(&self, edge: Edge<'index>) -> Result<&'index CompiledPackage, ResolverError> {
        let required = self.package(edge.dependency.package_id)?;
        let requirement = VersionReq::parse(&edge.dependency.requirement).map_err(|error| {
            ResolverError::new(
                ErrorCode::InvalidRequirement,
                format!(
                    "invalid requirement '{}' from '{}': {error}",
                    edge.dependency.requirement, edge.package.name
                ),
            )
        })?;
        let version = Version::parse(&required.version)
            .map_err(|error| ResolverError::new(ErrorCode::InvalidIndex, error.to_string()))?;
        if !requirement.matches(&version) {
            return Err(ResolverError::new(
                ErrorCode::VersionMismatch,
                format!(
                    "{} {} does not satisfy {} required by {}",
                    required.name, version, requirement, edge.package.name
                ),
            ));
        }
        Ok(required)
    }
}

fn cycle_error(id: u64) -> ResolverError {
    ResolverError::new(
        ErrorCode::DependencyCycle,
        format!("dependency cycle includes package id {id}"),
    )
}
