//! Application state management
//!
//! This module defines the global application state that is shared across
//! all handlers and services. It manages health status, Redis connections,
//! and inter-component communication.

use std::sync::{Arc, RwLock};
use std::time::Duration;
use tokio::sync::{Mutex, Notify};

use crate::models::HealthStatusState;
use redis::aio::ConnectionManager;
use rust_decimal::Decimal;

/// Global application state
///
/// This struct holds all shared state that needs to be accessed across
/// different parts of the application. It's designed to be cheaply cloneable
/// using Arc (Atomic Reference Counting).
#[derive(Clone)]
pub struct AppState {
    /// Health status of downstream services
    pub health_status: Arc<RwLock<HealthStatusState>>,

    /// Redis client for data persistence and queuing
    pub redis_conn_manager: ConnectionManager,

    /// Notification mechanism for processor state changes
    pub processor_notify: Arc<Notify>,

    /// Counter for round-robin payment distribution
    pub payment_counter: Arc<Mutex<u64>>,

    /// Static payment amount initialized on first payment request
    pub payment_amount: Arc<Mutex<Option<Decimal>>>,
}

impl AppState {
    /// Create new application state with Redis connection
    pub async fn new(redis_url: &str) -> Result<Self, redis::RedisError> {
        let client = redis::Client::open(redis_url)?;
        let redis_conn_manager = ConnectionManager::new(client).await?;
        Ok(Self {
            health_status: Arc::new(RwLock::new(HealthStatusState::default())),
            redis_conn_manager,
            processor_notify: Arc::new(Notify::new()),
            payment_counter: Arc::new(Mutex::new(0)),
            payment_amount: Arc::new(Mutex::new(None)),
        })
    }

    /// Update health status and notify waiting processors
    ///
    /// This method is thread-safe and will notify payment processors
    /// when the service becomes healthy.
    pub fn update_default_processor_health(&self, is_healthy: bool, min_response_time: Duration) {
        let was_healthy = self.is_default_processor_healthy();

        if let Ok(mut health) = self.health_status.write() {
            health.default_processor_healthy = is_healthy;
            health.min_response_time = min_response_time;
        }

        // Notify payment processors if service became healthy
        if !was_healthy && is_healthy {
            self.processor_notify.notify_one();
        }
    }

    /// Check if the default processor is currently healthy
    pub fn is_default_processor_healthy(&self) -> bool {
        if let Ok(health) = self.health_status.read() {
            health.default_processor_healthy
        } else {
            false
        }
    }
}
