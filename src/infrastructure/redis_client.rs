use chrono::Utc;
use redis::{AsyncCommands, aio::ConnectionManager};
use rust_decimal::Decimal;
use rust_decimal::prelude::{FromPrimitive, ToPrimitive};
use serde_json;

use crate::models::{Payment, PaymentProcessorStats};
use crate::state::AppState;

pub async fn store_payment(payment: Payment, app_state: &AppState) {
    let mut con = app_state.redis_conn_manager.clone();
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

    let value = serde_json::to_string(&payment).unwrap_or_default();
    let _: Result<(), redis::RedisError> = con.lpush(queue_name, value).await;

    let _: Result<(), redis::RedisError> = con
        .zadd(
            "payments_by_time",
            payment.correlation_id.clone(),
            timestamp,
        )
        .await;
}

pub async fn pop_payment_from_queue(app_state: &AppState, queues: &[String]) -> Option<Payment> {
    let mut con = app_state.redis_conn_manager.clone();
    let result: Result<Vec<String>, redis::RedisError> = con.brpop(queues, 1.0).await;

    if let Ok(payment_data) = result {
        if payment_data.len() >= 2 {
            let payment_json = &payment_data[1];
            return serde_json::from_str::<Payment>(payment_json).ok();
        }
    }
    None
}

pub async fn delete_payment_metadata(app_state: &AppState, correlation_id: &str) {
    let mut con = app_state.redis_conn_manager.clone();
    let key = format!("payment:{}", correlation_id);
    let _: Result<(), redis::RedisError> = con.del(key).await;
}

pub async fn requeue_payment_for_retry(app_state: &AppState, payment: Payment) {
    let mut con = app_state.redis_conn_manager.clone();
    let value = serde_json::to_string(&payment).unwrap_or_default();
    let _: Result<(), redis::RedisError> = con.lpush("payment_queue_shared", value).await;

    let failure_key = format!("payment_failure:{}", payment.correlation_id);
    let _: Result<(), redis::RedisError> = con.set_ex(failure_key, "retry", 3600).await;
}

pub async fn get_payment_stats_by_timerange(
    app_state: &AppState,
    from_timestamp: Option<i64>,
    to_timestamp: Option<i64>,
) -> (PaymentProcessorStats, PaymentProcessorStats) {
    let mut con = app_state.redis_conn_manager.clone();
    let payment_ids: Result<Vec<String>, redis::RedisError> = match (from_timestamp, to_timestamp) {
        (Some(from), Some(to)) => con.zrangebyscore("payments_by_time", from, to).await,
        (Some(from), None) => con.zrangebyscore("payments_by_time", from, "+inf").await,
        (None, Some(to)) => con.zrangebyscore("payments_by_time", "-inf", to).await,
        (None, None) => con.zrange("payments_by_time", 0, -1).await,
    };

    if let Ok(ids) = payment_ids {
        let total_requests = ids.len() as i64;
        let locked = app_state.payment_amount.lock().await;
        let amount = locked.unwrap_or(Decimal::ZERO);
        let total_amount = (amount * Decimal::from_i64(total_requests).unwrap_or(Decimal::ZERO))
            .to_f64()
            .unwrap_or(0.0);
        let default_stats = PaymentProcessorStats {
            total_requests,
            total_amount,
        };
        let fallback_stats = PaymentProcessorStats::default();
        return (default_stats, fallback_stats);
    }

    (
        PaymentProcessorStats::default(),
        PaymentProcessorStats::default(),
    )
}
