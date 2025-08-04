use chrono::{DateTime, Utc};
use rust_decimal::Decimal;
use std::time::Duration;
use tokio::sync::Mutex;

use crate::infrastructure::{http_client, redis_client};
use crate::models::*;
use crate::state::AppState;

pub async fn create_payment(app_state: &AppState, create_payment: CreatePayment) {
    let mut amt_lock = app_state.payment_amount.lock().await;
    if amt_lock.is_none() {
        *amt_lock = Some(create_payment.amount);
    }
    let amount = amt_lock.unwrap();
    drop(amt_lock);

    let payment = Payment {
        correlation_id: create_payment.correlation_id,
        amount,
    };
    let app_state_clone = app_state.clone();
    tokio::spawn(async move {
        redis_client::store_payment(payment, &app_state_clone).await;
    });
}

pub async fn get_payment_summary(
    app_state: &AppState,
    from_str: String,
    to_str: String,
) -> PaymentSummary {
    println!(
        "get_payment_summary called with from: {}, to: {}",
        from_str, to_str
    );

    let (default_stats, fallback_stats) = match (!from_str.is_empty(), !to_str.is_empty()) {
        (true, true) => {
            println!("Both from and to parameters provided");
            if let (Ok(from_time), Ok(to_time)) = (
                DateTime::parse_from_rfc3339(&from_str),
                DateTime::parse_from_rfc3339(&to_str),
            ) {
                println!("Successfully parsed from and to times");
                get_payment_stats_from_timerange(
                    app_state,
                    Some(from_time.timestamp()),
                    Some(to_time.timestamp()),
                )
                .await
            } else {
                println!("Failed to parse from and to times");
                (
                    PaymentProcessorStats::default(),
                    PaymentProcessorStats::default(),
                )
            }
        }
        (true, false) => {
            println!("Only from parameter provided");
            if let Ok(from_time) = DateTime::parse_from_rfc3339(&from_str) {
                println!("Successfully parsed from time");
                get_payment_stats_from_timerange(app_state, Some(from_time.timestamp()), None).await
            } else {
                println!("Failed to parse from time");
                (
                    PaymentProcessorStats::default(),
                    PaymentProcessorStats::default(),
                )
            }
        }
        (false, true) => {
            println!("Only to parameter provided");
            if let Ok(to_time) = DateTime::parse_from_rfc3339(&to_str) {
                println!("Successfully parsed to time");
                get_payment_stats_from_timerange(app_state, None, Some(to_time.timestamp())).await
            } else {
                println!("Failed to parse to time");
                (
                    PaymentProcessorStats::default(),
                    PaymentProcessorStats::default(),
                )
            }
        }
        (false, false) => {
            println!("No parameters provided, get all payments");
            get_payment_stats_from_timerange(app_state, None, None).await
        }
    };

    println!("Finished getting payment stats, returning summary");
    PaymentSummary {
        default: default_stats,
        fallback: fallback_stats,
    }
}

pub async fn payment_processor_task(app_state: AppState) {
    let instance_id = std::env::var("INSTANCE_ID").unwrap_or("1".to_string());
    let primary_queue = format!("payment_queue_{}", instance_id);
    let queues = vec![primary_queue, "payment_queue_shared".to_string()];

    loop {
        if !app_state.is_default_processor_healthy() {
            app_state.processor_notify.notified().await;
            continue;
        }

        if let Some(payment) = redis_client::pop_payment_from_queue(&app_state, &queues).await {
            process_payment(payment, &app_state).await;
        }
    }
}

async fn process_payment(payment: Payment, app_state: &AppState) {
    let endpoint = std::env::var("DEFAULT_PROCESSOR_ENDPOINT")
        .unwrap_or_else(|_| "http://payment-processor-default:8080".to_string());

    let min_response_time = if let Ok(health) = app_state.health_status.read() {
        health.min_response_time
    } else {
        Duration::from_millis(0)
    };

    let requested_at =
        Utc::now() - chrono::Duration::from_std(min_response_time).unwrap_or_default();

    let payload = CreatePaymentDownstream {
        correlation_id: payment.correlation_id.clone(),
        amount: payment.amount,
        requested_at,
    };

    let process_url = format!("{}/payments", endpoint);

    match http_client::post_json(&process_url, &payload).await {
        Ok(_) => {
            redis_client::delete_payment_metadata(&app_state, &payment.correlation_id).await;
        }
        Err(_) => {
            redis_client::requeue_payment_for_retry(&app_state, payment).await;
        }
    }
}

async fn get_payment_stats_from_timerange(
    app_state: &AppState,
    from_timestamp: Option<i64>,
    to_timestamp: Option<i64>,
) -> (PaymentProcessorStats, PaymentProcessorStats) {
    redis_client::get_payment_stats_by_timerange(app_state, from_timestamp, to_timestamp).await
}
