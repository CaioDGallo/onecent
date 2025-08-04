//! Redis client operations
//!
//! This module provides all Redis operations needed by the application.
//! It abstracts Redis specifics from the business logic.

use chrono::Utc;
use redis::{AsyncCommands, aio::ConnectionManager};
use rust_decimal::Decimal;
use rust_decimal::prelude::{FromPrimitive, ToPrimitive};
use serde_json;

use crate::models::{Payment, PaymentProcessorStats};
use crate::state::AppState;

/// Store a payment for background processing
///
/// Uses round-robin distribution between instance queues for load balancing.
/// Also stores correlation IDs in time-ordered set for time-based queries.
pub async fn store_payment(payment: Payment, app_state: &AppState) {
    let mut con = app_state.redis_conn_manager.clone();
    let now = Utc::now();
    let timestamp = now.timestamp();

    // Round-robin queue distribution
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

    // Store in processing queue
    let value = serde_json::to_string(&payment).unwrap_or_default();
    let _: Result<(), redis::RedisError> = con.lpush(queue_name, value).await;

    // Add to time-ordered set for range queries
    let _: Result<(), redis::RedisError> = con
        .zadd(
            "payments_by_time",
            payment.correlation_id.clone(),
            timestamp,
        )
        .await;
}

/// Pop next payment from queues using atomic BRPOP
///
/// Returns None if no payments available within timeout.
pub async fn pop_payment_from_queue(app_state: &AppState, queues: &[String]) -> Option<Payment> {
    let mut con = app_state.redis_conn_manager.clone();
    // BRPOP with 1 second timeout
    let result: Result<Vec<String>, redis::RedisError> = con.brpop(queues, 1.0).await;

    if let Ok(payment_data) = result {
        if payment_data.len() >= 2 {
            let payment_json = &payment_data[1];
            return serde_json::from_str::<Payment>(payment_json).ok();
        }
    }
    None
}

/// Delete payment metadata after successful processing
pub async fn delete_payment_metadata(app_state: &AppState, correlation_id: &str) {
    let mut con = app_state.redis_conn_manager.clone();
    let key = format!("payment:{}", correlation_id);
    let _: Result<(), redis::RedisError> = con.del(key).await;
}

/// Requeue payment for retry after processing failure
pub async fn requeue_payment_for_retry(app_state: &AppState, payment: Payment) {
    let mut con = app_state.redis_conn_manager.clone();
    let value = serde_json::to_string(&payment).unwrap_or_default();
    let _: Result<(), redis::RedisError> = con.lpush("payment_queue_shared", value).await;

    // Track failure for monitoring
    let failure_key = format!("payment_failure:{}", payment.correlation_id);
    let _: Result<(), redis::RedisError> = con.set_ex(failure_key, "retry", 3600).await;
}

/// Get payment statistics for a time range
///
/// Counts stored payments and computes total amount using static payment_amount
pub async fn get_payment_stats_by_timerange(
    app_state: &AppState,
    from_timestamp: Option<i64>,
    to_timestamp: Option<i64>,
) -> (PaymentProcessorStats, PaymentProcessorStats) {
    let mut con = app_state.redis_conn_manager.clone();
    // Fetch correlation IDs from sorted set within range
    let payment_ids: Result<Vec<String>, redis::RedisError> = match (from_timestamp, to_timestamp) {
        (Some(from), Some(to)) => con.zrangebyscore("payments_by_time", from, to).await,
        (Some(from), None) => con.zrangebyscore("payments_by_time", from, "+inf").await,
        (None, Some(to)) => con.zrangebyscore("payments_by_time", "-inf", to).await,
        (None, None) => con.zrange("payments_by_time", 0, -1).await,
    };

    if let Ok(ids) = payment_ids {
        // Determine count of stored payments
        let total_requests = ids.len() as i64;
        // Retrieve static payment amount
        let locked = app_state.payment_amount.lock().await;
        let amount = locked.unwrap_or(Decimal::ZERO);
        let total_amount = (amount * Decimal::from_i64(total_requests).unwrap_or(Decimal::ZERO))
            .to_f64()
            .unwrap_or(0.0);
        let default_stats = PaymentProcessorStats {
            total_requests,
            total_amount,
        };
        // No fallback processor usage in static-amount mode
        let fallback_stats = PaymentProcessorStats::default();
        return (default_stats, fallback_stats);
    }

    (
        PaymentProcessorStats::default(),
        PaymentProcessorStats::default(),
    )
}
