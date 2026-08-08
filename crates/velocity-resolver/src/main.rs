//! JSON stdin/stdout process entry point for the Velocity resolver sidecar.

use std::io::{self, Read, Write};

use velocity_resolver::{Request, ResolverError, Response, handle_request};

fn run() -> Response {
    let mut input = Vec::new();
    if let Err(error) = io::stdin().read_to_end(&mut input) {
        return Response::from_error(&ResolverError::index_io(error.to_string()));
    }
    let request: Request = match serde_json::from_slice(&input) {
        Ok(request) => request,
        Err(error) => {
            return Response::from_error(&ResolverError::invalid_request(error.to_string()));
        }
    };
    match handle_request(&request) {
        Ok(response) => response,
        Err(error) => Response::from_error(&error),
    }
}

fn main() {
    let response = run();
    let stdout = io::stdout();
    let mut output = stdout.lock();
    if serde_json::to_writer(&mut output, &response).is_ok() {
        let _result = output.write_all(b"\n");
    }
}
