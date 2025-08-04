use axum::{
    Json,
    extract::{Query, State},
    http::StatusCode,
};
use std::collections::HashMap;

use crate::models::*;
use crate::services::{health_service, payment_service};
use crate::state::AppState;

pub async fn root() -> &'static str {
    "Hello, World!"
}

pub async fn health_sync(
    State(state): State<AppState>,
    Json(payload): Json<HealthStatus>,
) -> StatusCode {
    health_service::sync_health_status(&state, payload).await;
    StatusCode::OK
}

pub async fn payments_summary(
    State(state): State<AppState>,
    Query(params): Query<HashMap<String, String>>,
) -> (StatusCode, Json<PaymentSummary>) {
    let from_str = params.get("from").cloned().unwrap_or_default();
    let to_str = params.get("to").cloned().unwrap_or_default();

    let summary = payment_service::get_payment_summary(&state, from_str, to_str).await;
    (StatusCode::OK, Json(summary))
}

pub async fn create_payment(
    State(state): State<AppState>,
    Json(payload): Json<CreatePayment>,
) -> StatusCode {
    payment_service::create_payment(&state, payload).await;
    StatusCode::CREATED
}
