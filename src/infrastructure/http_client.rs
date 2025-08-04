//! HTTP client operations
//!
//! This module provides HTTP client functionality for communicating with
//! downstream services and other application instances.

use std::time::Duration;

use once_cell::sync::Lazy;
use serde::Serialize;

// Create a global reqwest client to be reused across all requests.
//
// `once_cell` ensures that the client is initialized only once.
static CLIENT: Lazy<reqwest::Client> = Lazy::new(|| {
    reqwest::Client::builder()
        .pool_max_idle_per_host(50)
        .connect_timeout(Duration::from_secs(5))
        .timeout(Duration::from_secs(30))
        .build()
        .expect("Failed to build reqwest client")
});

/// Post JSON data to an endpoint
///
/// Generic function that can send any serializable data as JSON.
pub async fn post_json<T: Serialize>(
    url: &str,
    payload: &T,
) -> Result<reqwest::Response, reqwest::Error> {
    CLIENT.post(url).json(payload).send().await
}

/// Get data from an endpoint
pub async fn get(url: &str) -> Result<reqwest::Response, reqwest::Error> {
    CLIENT.get(url).send().await
}
