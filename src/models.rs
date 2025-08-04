//! Domain models and data structures
//!
//! This module contains all the core data types used throughout the application.
//! These are "pure" data structures without business logic.

use chrono::{DateTime, Utc};
use rust_decimal::Decimal;
use serde::{Deserialize, Serialize};
use std::time::Duration;

/// Payment creation request from clients
#[derive(Deserialize)]
pub struct CreatePayment {
    #[serde(rename = "correlationId")]
    pub correlation_id: String,
    pub amount: Decimal,
}

/// Payment data sent to downstream processors
#[derive(Serialize)]
pub struct CreatePaymentDownstream {
    #[serde(rename = "correlationId")]
    pub correlation_id: String,
    pub amount: Decimal,
    #[serde(rename = "requestedAt")]
    pub requested_at: DateTime<Utc>,
}

/// Internal payment representation
#[derive(Serialize, Deserialize)]
pub struct Payment {
    #[serde(rename = "correlationId")]
    pub correlation_id: String,
    pub amount: Decimal,
}

/// Health status from downstream services
#[derive(Deserialize, Serialize)]
pub struct HealthStatus {
    pub failing: bool,
    #[serde(rename = "minResponseTime")]
    pub min_response_time: i64,
}

/// Payment processor statistics
#[derive(Serialize, Default)]
pub struct PaymentProcessorStats {
    #[serde(rename = "totalRequests")]
    pub total_requests: i64,
    #[serde(rename = "totalAmount")]
    pub total_amount: f64,
}

/// Summary of all payment processors
#[derive(Serialize)]
pub struct PaymentSummary {
    pub default: PaymentProcessorStats,
    pub fallback: PaymentProcessorStats,
}

/// Internal health state tracking
#[derive(Default, Debug)]
pub struct HealthStatusState {
    pub default_processor_healthy: bool,
    pub min_response_time: Duration,
}
