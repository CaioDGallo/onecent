//! Health monitoring and status management
//!
//! This service handles health checking of downstream services and manages
//! the health state across multiple application instances.

use chrono::{DateTime, Utc};
use std::time::Duration;
use tokio::time;

use crate::infrastructure::http_client;
use crate::models::HealthStatus;
use crate::state::AppState;

/// Sync health status from primary instance
///
/// Called when receiving health updates from the primary health checker.
pub async fn sync_health_status(app_state: &AppState, health_status: HealthStatus) {
    let min_response_time = Duration::from_millis(health_status.min_response_time as u64);
    app_state.update_default_processor_health(!health_status.failing, min_response_time);
}

/// Background task for health monitoring
///
/// Only runs on INSTANCE_ID=1 to avoid duplicate health checks.
/// Checks downstream service health every 5 seconds and notifies other instances.
pub async fn health_monitor_task(app_state: AppState) {
    let instance_id = std::env::var("INSTANCE_ID").unwrap_or("1".to_string());
    if instance_id != "1" {
        return; // Only primary instance performs health checks
    }

    let endpoint =
        std::env::var("DEFAULT_PROCESSOR_ENDPOINT").expect("DEFAULT_PROCESSOR_ENDPOINT required");
    let health_url = format!("{}/payments/service-health", endpoint);

    let mut interval = time::interval(Duration::from_secs(5));

    loop {
        interval.tick().await;

        if let Ok(health_status) = check_downstream_health(&health_url).await {
            // Update local state
            let min_response_time = Duration::from_millis(health_status.min_response_time as u64);
            app_state.update_default_processor_health(!health_status.failing, min_response_time);

            // Notify other instances
            notify_instance_2(&health_status).await;
        }
    }
}

/// Check health of downstream payment processor
async fn check_downstream_health(health_url: &str) -> Result<HealthStatus, String> {
    let client = reqwest::Client::new();
    let response = client
        .get(health_url)
        .send()
        .await
        .map_err(|e| format!("HTTP request failed: {}", e))?;
    let health_status = response
        .json::<HealthStatus>()
        .await
        .map_err(|e| format!("JSON parsing failed: {}", e))?;
    Ok(health_status)
}

/// Notify instance 2 of health status changes
async fn notify_instance_2(health_status: &HealthStatus) {
    let _ = http_client::post_json("http://rust-app-2:8080/health-sync", health_status).await;
}
