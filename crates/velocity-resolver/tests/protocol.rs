//! End-to-end stdin/stdout binary protocol test.
#![allow(
    clippy::expect_used,
    reason = "test fixtures and failure assertions may panic"
)]

use bincode::Options;
use std::{
    fs,
    io::Write,
    process::{Command, Stdio},
};
use velocity_resolver::CompiledIndex;

#[test]
fn binary_reads_one_request_and_writes_one_ok_response() {
    // Given
    let unique = format!(
        "velocity-resolver-{}-{}.idx.zst",
        std::process::id(),
        line!()
    );
    let path = std::env::temp_dir().join(unique);
    let index = CompiledIndex {
        format_version: 1,
        packages: Vec::new(),
        aliases: Vec::new(),
    };
    let payload = bincode::DefaultOptions::new()
        .with_fixint_encoding()
        .serialize(&index)
        .expect("fixture serializes");
    let mut raw = b"VLTIDX1\0".to_vec();
    raw.extend_from_slice(&1_u32.to_le_bytes());
    raw.extend(payload);
    fs::write(
        &path,
        zstd::stream::encode_all(raw.as_slice(), 1).expect("fixture compresses"),
    )
    .expect("fixture writes");
    let request = serde_json::json!({"protocol":1,"index_path":path,"target":"x86_64-unknown-linux-gnu","roots":[]});
    let mut child = Command::new(env!("CARGO_BIN_EXE_velocity-resolver"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .expect("binary starts");

    // When
    child
        .stdin
        .take()
        .expect("stdin exists")
        .write_all(request.to_string().as_bytes())
        .expect("request writes");
    let output = child.wait_with_output().expect("binary exits");

    // Then
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response is JSON");
    assert_eq!(
        response,
        serde_json::json!({"protocol":1,"status":"ok","packages":[]})
    );
    let _cleanup = fs::remove_file(path);
}

#[test]
fn binary_rejects_unknown_request_fields_as_invalid_request() {
    // Given
    let request = serde_json::json!({
        "protocol": 1,
        "index_path": "unused.idx.zst",
        "target": "x86_64-unknown-linux-gnu",
        "roots": [],
        "unexpected": true,
    });
    let mut child = Command::new(env!("CARGO_BIN_EXE_velocity-resolver"))
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .expect("binary starts");

    // When
    child
        .stdin
        .take()
        .expect("stdin exists")
        .write_all(request.to_string().as_bytes())
        .expect("request writes");
    let output = child.wait_with_output().expect("binary exits");

    // Then
    let response: serde_json::Value =
        serde_json::from_slice(&output.stdout).expect("response is JSON");
    assert_eq!(
        response.get("status"),
        Some(&serde_json::json!("error")),
        "unknown request fields must not be accepted",
    );
    assert_eq!(
        response.get("error").and_then(|error| error.get("code")),
        Some(&serde_json::json!("invalid_request")),
    );
}
