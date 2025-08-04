use axum::{
    Json, Router,
    extract::{Query, State},
    http::StatusCode,
    routing::{get, post},
};
use chrono::{DateTime, Utc};
use redis::AsyncCommands;
use rust_decimal::Decimal;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::{Arc, RwLock};
use std::time::Duration;
use tokio::sync::{Mutex, Notify};

#[derive(Clone)]
struct AppState {
    health_status: Arc<RwLock<HealthStatusState>>,
    redis_client: redis::Client,
    processor_notify: Arc<Notify>,
    payment_counter: Arc<Mutex<u64>>,
}

#[derive(Default, Debug)]
struct HealthStatusState {
    default_processor_healthy: bool,
    min_response_time: Duration,
}

impl AppState {
    fn new(redis_url: &str) -> Result<Self, redis::RedisError> {
        let redis_client = redis::Client::open(redis_url)?;
        Ok(Self {
            health_status: Arc::new(RwLock::new(HealthStatusState::default())),
            redis_client,
            processor_notify: Arc::new(Notify::new()),
            payment_counter: Arc::new(Mutex::new(0)),
        })
    }

    fn update_default_processor_health(&self, is_healthy: bool, min_response_time: Duration) {
        let was_healthy = self.is_default_processor_healthy();

        if let Ok(mut health) = self.health_status.write() {
            health.default_processor_healthy = is_healthy;
            health.min_response_time = min_response_time;
        }

        if !was_healthy && is_healthy {
            self.processor_notify.notify_one();
        }
    }

    fn is_default_processor_healthy(&self) -> bool {
        if let Ok(health) = self.health_status.read() {
            health.default_processor_healthy
        } else {
            false
        }
    }
}

async fn payment_processor_task(app_state: AppState) {
    let instance_id = std::env::var("INSTANCE_ID").unwrap_or("1".to_string());
    let primary_queue = format!("payment_queue_{}", instance_id);
    let queues = vec![primary_queue, "payment_queue_shared".to_string()];

    loop {
        if !app_state.is_default_processor_healthy() {
            app_state.processor_notify.notified().await;
            continue;
        }

        if let Ok(mut con) = app_state
            .redis_client
            .get_multiplexed_async_connection()
            .await
        {
            let result: Result<Vec<String>, redis::RedisError> = con.brpop(&queues, 1.0).await;

            if let Ok(payment_data) = result {
                if payment_data.len() >= 2 {
                    let _queue_name = &payment_data[0];
                    let payment_json = &payment_data[1];

                    if let Ok(payment) = serde_json::from_str::<Payment>(payment_json) {
                        process_payment(payment, &app_state).await;
                    }
                }
            }
        }
    }
}

async fn process_payment(payment: Payment, app_state: &AppState) {
    let endpoint = std::env::var("DEFAULT_PROCESSOR_ENDPOINT")
        .unwrap_or_else(|_| "http://payment-processor-default:8080".to_string());

    let client = reqwest::Client::new();
    let process_url = format!("{}/payments", endpoint);

    let min_response_time = if let Ok(health) = app_state.health_status.read() {
        health.min_response_time
    } else {
        Duration::from_millis(0)
    };

    let requested_at =
        Utc::now() - chrono::Duration::from_std(min_response_time).unwrap_or_default();

    let payload = CreatePaymentDownstream {
        correlationId: payment.correlationId.clone(),
        amount: payment.amount,
        requestedAt: requested_at,
    };

    if let Ok(_response) = client.post(&process_url).json(&payload).send().await {
        if let Ok(mut con) = app_state
            .redis_client
            .get_multiplexed_async_connection()
            .await
        {
            let key = format!("payment:{}", payment.correlationId);
            let _: Result<(), redis::RedisError> = con.del(key).await;
        }
    } else {
        if let Ok(mut con) = app_state
            .redis_client
            .get_multiplexed_async_connection()
            .await
        {
            let value = serde_json::to_string(&payment).unwrap_or_default();
            let _: Result<(), redis::RedisError> = con.lpush("payment_queue_shared", value).await;

            let failure_key = format!("payment_failure:{}", payment.correlationId);
            let _: Result<(), redis::RedisError> = con.set_ex(failure_key, "retry", 3600).await;
        }
    }
}

#[tokio::main]
async fn main() {
    let redis_url =
        std::env::var("REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".to_string());
    let app_state = AppState::new(&redis_url).expect("Failed to create AppState");

    let health_monitor_state = app_state.clone();
    tokio::spawn(health_monitor_task(health_monitor_state));

    let payment_processor_state = app_state.clone();
    tokio::spawn(payment_processor_task(payment_processor_state));

    let app = Router::new()
        .route("/", get(root))
        .route("/health-sync", post(health_sync))
        .route("/payments-summary", get(payments_summary))
        .route("/payments", post(create_payment))
        .with_state(app_state);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await.unwrap();
    axum::serve(listener, app).await.unwrap();
}

async fn health_monitor_task(app_state: AppState) {
    let instance_id = std::env::var("INSTANCE_ID").unwrap_or("1".to_string());
    if instance_id != "1" {
        return;
    }

    let client = reqwest::Client::new();
    let endpoint =
        std::env::var("DEFAULT_PROCESSOR_ENDPOINT").expect("DEFAULT_PROCESSOR_ENDPOINT required");
    let health_url = format!("{}/payments/service-health", endpoint);

    let mut interval = tokio::time::interval(Duration::from_secs(5));

    loop {
        interval.tick().await;

        if let Ok(response) = client.get(&health_url).send().await {
            if let Ok(health_status) = response.json::<HealthStatus>().await {
                let min_response_time = Duration::from_millis(health_status.minResponseTime as u64);
                app_state
                    .update_default_processor_health(!health_status.failing, min_response_time);

                let _ = client
                    .post("http://rust-app-2:8080/health-sync")
                    .json(&health_status)
                    .send()
                    .await;
            }
        }
    }
}

async fn get_payment_stats_from_timerange(
    app_state: &AppState,
    from_timestamp: Option<i64>,
    to_timestamp: Option<i64>,
) -> (PaymentProcessorStats, PaymentProcessorStats) {
    if let Ok(mut con) = app_state
        .redis_client
        .get_multiplexed_async_connection()
        .await
    {
        let payment_ids: Result<Vec<String>, redis::RedisError> =
            match (from_timestamp, to_timestamp) {
                (Some(from), Some(to)) => con.zrangebyscore("payments_by_time", from, to).await,
                (Some(from), None) => con.zrangebyscore("payments_by_time", from, "+inf").await,
                (None, Some(to)) => con.zrangebyscore("payments_by_time", "-inf", to).await,
                (None, None) => con.zrange("payments_by_time", 0, -1).await,
            };

        if let Ok(ids) = payment_ids {
            let mut default_requests = Decimal::ZERO;
            let mut default_amount = Decimal::ZERO;
            let mut fallback_requests = Decimal::ZERO;
            let mut fallback_amount = Decimal::ZERO;

            for id in ids {
                let payment_key = format!("payment_meta:{}", id);
                if let Ok(metadata_str) = con.get::<String, String>(payment_key).await {
                    if let Ok(metadata) = serde_json::from_str::<serde_json::Value>(&metadata_str) {
                        // Try to parse as string first (new format), fallback to f64 (old format)
                        let amount = metadata["amount"].as_str()
                            .and_then(|s| s.parse::<Decimal>().ok())
                            .or_else(|| metadata["amount"].as_f64().and_then(|f| Decimal::try_from(f).ok()))
                            .unwrap_or(Decimal::ZERO);
                        let processor = metadata["processor"].as_str().unwrap_or("default");

                        match processor {
                            "default" => {
                                default_requests += Decimal::ONE;
                                default_amount += amount;
                            }
                            "fallback" => {
                                fallback_requests += Decimal::ONE;
                                fallback_amount += amount;
                            }
                            _ => {
                                default_requests += Decimal::ONE;
                                default_amount += amount;
                            }
                        }
                    }
                }
            }

            return (
                PaymentProcessorStats {
                    totalRequests: default_requests,
                    totalAmount: default_amount,
                },
                PaymentProcessorStats {
                    totalRequests: fallback_requests,
                    totalAmount: fallback_amount,
                },
            );
        }
    }

    (
        PaymentProcessorStats::default(),
        PaymentProcessorStats::default(),
    )
}

async fn root() -> &'static str {
    "Hello, World!"
}

async fn health_sync(
    State(state): State<AppState>,
    Json(payload): Json<HealthStatus>,
) -> StatusCode {
    let min_response_time = Duration::from_millis(payload.minResponseTime as u64);
    state.update_default_processor_health(!payload.failing, min_response_time);
    StatusCode::OK
}

async fn payments_summary(
    State(state): State<AppState>,
    Query(params): Query<HashMap<String, String>>,
) -> (StatusCode, Json<PaymentSummary>) {
    let from_str = params.get("from").cloned().unwrap_or_default();
    let to_str = params.get("to").cloned().unwrap_or_default();

    let (default_stats, fallback_stats) = match (!from_str.is_empty(), !to_str.is_empty()) {
        (true, true) => {
            if let (Ok(from_time), Ok(to_time)) = (
                DateTime::parse_from_rfc3339(&from_str),
                DateTime::parse_from_rfc3339(&to_str),
            ) {
                get_payment_stats_from_timerange(
                    &state,
                    Some(from_time.timestamp()),
                    Some(to_time.timestamp()),
                )
                .await
            } else {
                (
                    PaymentProcessorStats::default(),
                    PaymentProcessorStats::default(),
                )
            }
        }
        (true, false) => {
            if let Ok(from_time) = DateTime::parse_from_rfc3339(&from_str) {
                get_payment_stats_from_timerange(&state, Some(from_time.timestamp()), None).await
            } else {
                (
                    PaymentProcessorStats::default(),
                    PaymentProcessorStats::default(),
                )
            }
        }
        (false, true) => {
            if let Ok(to_time) = DateTime::parse_from_rfc3339(&to_str) {
                get_payment_stats_from_timerange(&state, None, Some(to_time.timestamp())).await
            } else {
                (
                    PaymentProcessorStats::default(),
                    PaymentProcessorStats::default(),
                )
            }
        }
        (false, false) => get_payment_stats_from_timerange(&state, None, None).await,
    };

    let payment_summary_payload = PaymentSummary {
        default: default_stats,
        fallback: fallback_stats,
    };

    (StatusCode::OK, Json(payment_summary_payload))
}

async fn create_payment(
    State(state): State<AppState>,
    Json(payload): Json<CreatePayment>,
) -> StatusCode {
    let payment_payload = Payment {
        correlationId: payload.correlationId.clone(),
        amount: payload.amount,
    };

    let app_state = state.clone();
    tokio::spawn(store_payment(payment_payload, app_state));

    StatusCode::CREATED
}

async fn store_payment(payload: Payment, app_state: AppState) -> () {
    if let Ok(mut con) = app_state
        .redis_client
        .get_multiplexed_async_connection()
        .await
    {
        let now = Utc::now();
        let timestamp = now.timestamp();

        let counter = {
            let mut counter_guard = app_state.payment_counter.lock().await;
            *counter_guard += 1;
            *counter_guard
        };

        let queue_name = if counter % 2 == 1 {
            "payment_queue_1"
        } else {
            "payment_queue_2"
        };

        let value = serde_json::to_string(&payload).unwrap_or_default();
        let _: Result<(), redis::RedisError> = con.lpush(queue_name, value).await;

        let payment_key = format!("payment_meta:{}", payload.correlationId);
        let payment_metadata = serde_json::json!({
            "amount": payload.amount.to_string(), // Store as string to maintain precision
            "timestamp": timestamp,
            "processor": "default"
        });
        let _: Result<(), redis::RedisError> = con
            .set_ex(payment_key, payment_metadata.to_string(), 86400)
            .await;

        let _: Result<(), redis::RedisError> = con
            .zadd("payments_by_time", payload.correlationId.clone(), timestamp)
            .await;
    }
}

#[derive(Deserialize)]
struct CreatePayment {
    correlationId: String,
    amount: Decimal,
}

#[derive(Serialize)]
struct CreatePaymentDownstream {
    correlationId: String,
    amount: Decimal,
    requestedAt: DateTime<Utc>,
}

#[derive(Deserialize, Serialize)]
struct HealthStatus {
    failing: bool,
    minResponseTime: i64,
}

#[derive(Serialize, Deserialize)]
struct Payment {
    correlationId: String,
    amount: Decimal,
}

#[derive(Serialize)]
struct PaymentSummary {
    default: PaymentProcessorStats,
    fallback: PaymentProcessorStats,
}

#[derive(Serialize, Default)]
struct PaymentProcessorStats {
    totalRequests: Decimal,
    totalAmount: Decimal,
}
