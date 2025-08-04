use std::sync::{Arc, RwLock};
use std::time::Duration;
use tokio::sync::{Mutex, Notify};

use crate::models::HealthStatusState;
use redis::aio::ConnectionManager;
use rust_decimal::Decimal;

#[derive(Clone)]
pub struct AppState {
    pub health_status: Arc<RwLock<HealthStatusState>>,

    pub redis_conn_manager: ConnectionManager,

    pub processor_notify: Arc<Notify>,

    pub payment_counter: Arc<Mutex<u64>>,

    pub payment_amount: Arc<Mutex<Option<Decimal>>>,
}

impl AppState {
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

    pub fn update_default_processor_health(&self, is_healthy: bool, min_response_time: Duration) {
        let was_healthy = self.is_default_processor_healthy();

        if let Ok(mut health) = self.health_status.write() {
            health.default_processor_healthy = is_healthy;
            health.min_response_time = min_response_time;
        }

        if !was_healthy && is_healthy {
            self.processor_notify.notify_one();
        }
    }

    pub fn is_default_processor_healthy(&self) -> bool {
        if let Ok(health) = self.health_status.read() {
            health.default_processor_healthy
        } else {
            false
        }
    }
}
