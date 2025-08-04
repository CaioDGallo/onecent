use std::time::Duration;

use once_cell::sync::Lazy;
use serde::Serialize;

static CLIENT: Lazy<reqwest::Client> = Lazy::new(|| {
    reqwest::Client::builder()
        .pool_max_idle_per_host(50)
        .connect_timeout(Duration::from_secs(5))
        .timeout(Duration::from_secs(30))
        .build()
        .expect("Failed to build reqwest client")
});

pub async fn post_json<T: Serialize>(
    url: &str,
    payload: &T,
) -> Result<reqwest::Response, reqwest::Error> {
    CLIENT.post(url).json(payload).send().await
}

pub async fn get(url: &str) -> Result<reqwest::Response, reqwest::Error> {
    CLIENT.get(url).send().await
}
