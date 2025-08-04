//! HTTP request handlers
//!
//! This module contains all the HTTP endpoint handlers. Each handler is responsible
//! for extracting data from HTTP requests, calling the appropriate services, and
//! returning HTTP responses.

use axum::{
    Json,
    extract::{Query, State},
    http::StatusCode,
};
use std::collections::HashMap;

use crate::models::*;
use crate::services::{health_service, payment_service};
use crate::state::AppState;

/// Root endpoint - simple health check
pub async fn root() -> &'static str {
    "Hello, World!"
}

/// Sync health status between instances
///
/// This endpoint receives health updates from the primary instance (INSTANCE_ID=1)
/// and updates the local health state accordingly.
pub async fn health_sync(
    State(state): State<AppState>,
    Json(payload): Json<HealthStatus>,
) -> StatusCode {
    health_service::sync_health_status(&state, payload).await;
    StatusCode::OK
}

/// Get payment summary with optional time filtering
///
/// Query parameters:
/// - `from`: Start time in RFC3339 format (optional)
/// - `to`: End time in RFC3339 format (optional)
/// - No params: Returns all payments
/// - One param: Returns payments from/to that time
/// - Both params: Returns payments in the time range
pub async fn payments_summary(
    State(state): State<AppState>,
    Query(params): Query<HashMap<String, String>>,
) -> (StatusCode, Json<PaymentSummary>) {
    let from_str = params.get("from").cloned().unwrap_or_default();
    let to_str = params.get("to").cloned().unwrap_or_default();

    let summary = payment_service::get_payment_summary(&state, from_str, to_str).await;
    (StatusCode::OK, Json(summary))
}

/// Create a new payment
///
/// Accepts payment data (amount used only on the first request), stores it for async processing, and returns immediately.
/// The actual payment processing happens in the background.
pub async fn create_payment(
    State(state): State<AppState>,
    Json(payload): Json<CreatePayment>,
) -> StatusCode {
    payment_service::create_payment(&state, payload).await;
    StatusCode::CREATED
}
