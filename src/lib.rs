//! OnePercent Payment Service Library
//!
//! This library provides all the core functionality for the payment service.
//! It can be used independently of the main binary for testing or integration
//! into other applications.

pub mod handlers;
pub mod infrastructure;
pub mod models;
pub mod services;
pub mod state;

// Re-export commonly used types for convenience
pub use models::*;
pub use state::AppState;
