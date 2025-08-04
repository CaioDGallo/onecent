use axum::{Router, routing::get, routing::post};

mod handlers;
mod infrastructure;
mod models;
mod services;
mod state;

use handlers::*;
use services::{health_service, payment_service};
use state::AppState;

#[tokio::main]
async fn main() {
    let redis_url =
        std::env::var("REDIS_URL").unwrap_or_else(|_| "redis://127.0.0.1:6379".to_string());

    let app_state = AppState::new(&redis_url)
        .await
        .expect("Failed to create AppState");

    spawn_background_tasks(app_state.clone()).await;

    let app = create_router(app_state);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080")
        .await
        .expect("Failed to bind to port 8080");

    println!("service starting on port 8080");

    axum::serve(listener, app)
        .await
        .expect("Failed to start HTTP server");
}

fn create_router(app_state: AppState) -> Router {
    Router::new()
        .route("/", get(root))
        .route("/health-sync", post(health_sync))
        .route("/payments-summary", get(payments_summary))
        .route("/payments", post(create_payment))
        .with_state(app_state)
}

async fn spawn_background_tasks(app_state: AppState) {
    let health_monitor_state = app_state.clone();
    tokio::spawn(health_service::health_monitor_task(health_monitor_state));

    let payment_processor_state = app_state.clone();
    tokio::spawn(payment_service::payment_processor_task(
        payment_processor_state,
    ));

    println!("background tasks started");
}
