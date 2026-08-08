use super::bounds::{
    ARTIFACTS_TOTAL, AggregateLimit, BINARIES_TOTAL, CAPABILITIES_TOTAL, CONFLICTS_TOTAL,
    DEPENDENCIES_TOTAL, MAX_ALIASES, MAX_ARTIFACTS_PER_PACKAGE, MAX_BINARIES_PER_ARTIFACT,
    MAX_CAPABILITIES_PER_PACKAGE, MAX_CONFLICTS_PER_PACKAGE, MAX_DEPENDENCIES_PER_PACKAGE,
    MAX_PACKAGES, MAX_STRING_BYTES, MAX_TARGETS_PER_PREDICATE, TARGETS_TOTAL, resource_limit,
};
use crate::{ErrorCode, ResolverError};

#[derive(Default)]
struct Totals {
    dependencies: usize,
    artifacts: usize,
    binaries: usize,
    capabilities: usize,
    conflicts: usize,
    targets: usize,
}

pub(super) fn validate(payload: &[u8]) -> Result<(), ResolverError> {
    let mut decoder = Decoder {
        payload,
        offset: 0,
        totals: Totals::default(),
    };
    decoder.index()?;
    if decoder.offset != payload.len() {
        return Err(invalid("index payload has trailing bytes"));
    }
    Ok(())
}

struct Decoder<'a> {
    payload: &'a [u8],
    offset: usize,
    totals: Totals,
}

impl Decoder<'_> {
    fn index(&mut self) -> Result<(), ResolverError> {
        self.u32()?;
        let packages = self.count(MAX_PACKAGES, "package count")?;
        for _ in 0..packages {
            self.package()?;
        }
        let aliases = self.count(MAX_ALIASES, "alias count")?;
        for _ in 0..aliases {
            self.text("alias")?;
            self.u64()?;
        }
        Ok(())
    }

    fn package(&mut self) -> Result<(), ResolverError> {
        self.u64()?;
        self.text("package name")?;
        self.text("package version")?;
        self.text("package description")?;
        self.optional_text("package homepage")?;
        self.optional_text("package license")?;
        let capabilities = self.count(MAX_CAPABILITIES_PER_PACKAGE, "capabilities per package")?;
        add_total(
            &mut self.totals.capabilities,
            capabilities,
            CAPABILITIES_TOTAL,
        )?;
        for _ in 0..capabilities {
            self.text("capability")?;
        }
        let conflicts = self.count(MAX_CONFLICTS_PER_PACKAGE, "conflicts per package")?;
        add_total(&mut self.totals.conflicts, conflicts, CONFLICTS_TOTAL)?;
        for _ in 0..conflicts {
            self.text("conflict")?;
        }
        let dependencies = self.count(MAX_DEPENDENCIES_PER_PACKAGE, "dependencies per package")?;
        add_total(
            &mut self.totals.dependencies,
            dependencies,
            DEPENDENCIES_TOTAL,
        )?;
        for _ in 0..dependencies {
            self.dependency()?;
        }
        let artifacts = self.count(MAX_ARTIFACTS_PER_PACKAGE, "artifacts per package")?;
        add_total(&mut self.totals.artifacts, artifacts, ARTIFACTS_TOTAL)?;
        for _ in 0..artifacts {
            self.artifact()?;
        }
        Ok(())
    }

    fn dependency(&mut self) -> Result<(), ResolverError> {
        self.u64()?;
        self.text("dependency requirement")?;
        let targets = self.count(MAX_TARGETS_PER_PREDICATE, "targets per predicate")?;
        add_total(&mut self.totals.targets, targets, TARGETS_TOTAL)?;
        for _ in 0..targets {
            self.text("predicate target")?;
        }
        Ok(())
    }

    fn artifact(&mut self) -> Result<(), ResolverError> {
        self.text("artifact target")?;
        self.text("artifact URL")?;
        self.text("artifact SHA-256")?;
        self.text("artifact archive")?;
        self.u32()?;
        let binaries = self.count(MAX_BINARIES_PER_ARTIFACT, "binaries per artifact")?;
        add_total(&mut self.totals.binaries, binaries, BINARIES_TOTAL)?;
        for _ in 0..binaries {
            self.text("binary source")?;
            self.text("binary name")?;
        }
        Ok(())
    }

    fn optional_text(&mut self, label: &str) -> Result<(), ResolverError> {
        match self.u8()? {
            0 => Ok(()),
            1 => self.text(label),
            _ => Err(invalid("index option tag is invalid")),
        }
    }

    fn text(&mut self, label: &str) -> Result<(), ResolverError> {
        let length = self.count(MAX_STRING_BYTES, label)?;
        self.skip(length)
    }

    fn count(&mut self, maximum: usize, label: &str) -> Result<usize, ResolverError> {
        let count = self.u64()?;
        let maximum = u64::try_from(maximum).map_err(|_| resource_limit(label))?;
        if count > maximum {
            return Err(resource_limit(label));
        }
        usize::try_from(count).map_err(|_| resource_limit(label))
    }

    fn u8(&mut self) -> Result<u8, ResolverError> {
        Ok(self.read::<1>()?[0])
    }

    fn u32(&mut self) -> Result<u32, ResolverError> {
        Ok(u32::from_le_bytes(self.read()?))
    }

    fn u64(&mut self) -> Result<u64, ResolverError> {
        Ok(u64::from_le_bytes(self.read()?))
    }

    fn skip(&mut self, count: usize) -> Result<(), ResolverError> {
        let end = self.offset.checked_add(count).ok_or_else(truncated)?;
        if end > self.payload.len() {
            return Err(truncated());
        }
        self.offset = end;
        Ok(())
    }

    fn read<const N: usize>(&mut self) -> Result<[u8; N], ResolverError> {
        let end = self.offset.checked_add(N).ok_or_else(truncated)?;
        let bytes: [u8; N] = self
            .payload
            .get(self.offset..end)
            .and_then(|bytes| bytes.try_into().ok())
            .ok_or_else(truncated)?;
        self.offset = end;
        Ok(bytes)
    }
}

fn add_total(total: &mut usize, count: usize, limit: AggregateLimit) -> Result<(), ResolverError> {
    *total = total
        .checked_add(count)
        .ok_or_else(|| resource_limit(limit.label))?;
    if *total > limit.maximum {
        return Err(resource_limit(limit.label));
    }
    Ok(())
}

fn invalid(message: &'static str) -> ResolverError {
    ResolverError::new(ErrorCode::InvalidIndex, message)
}

fn truncated() -> ResolverError {
    invalid("index payload is truncated")
}
