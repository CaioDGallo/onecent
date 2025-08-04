use chrono::{DateTime, Utc};
use rust_decimal::Decimal;
use serde::{Deserialize, Serialize};
use std::time::Duration;

#[derive(Deserialize)]
pub struct CreatePayment {
    #[serde(rename = "correlationId")]
    pub correlation_id: String,
    pub amount: Decimal,
}

#[derive(Serialize)]
pub struct CreatePaymentDownstream {
    #[serde(rename = "correlationId")]
    pub correlation_id: String,
    pub amount: Decimal,
    #[serde(rename = "requestedAt")]
    pub requested_at: DateTime<Utc>,
}

#[derive(Serialize, Deserialize)]
pub struct Payment {
    #[serde(rename = "correlationId")]
    pub correlation_id: String,
    pub amount: Decimal,
}

#[derive(Deserialize, Serialize)]
pub struct HealthStatus {
    pub failing: bool,
    #[serde(rename = "minResponseTime")]
    pub min_response_time: i64,
}

#[derive(Serialize, Default)]
pub struct PaymentProcessorStats {
    #[serde(rename = "totalRequests")]
    pub total_requests: i64,
    #[serde(rename = "totalAmount")]
    pub total_amount: f64,
}

#[derive(Serialize)]
pub struct PaymentSummary {
    pub default: PaymentProcessorStats,
    pub fallback: PaymentProcessorStats,
}

#[derive(Default, Debug)]
pub struct HealthStatusState {
    pub default_processor_healthy: bool,
    pub min_response_time: Duration,
}
