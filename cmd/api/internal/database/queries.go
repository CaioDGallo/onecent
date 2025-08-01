package database

const (
	InsertPayment        = "INSERT INTO payment_log (idempotency_key, payment_processor, amount, fee, requested_at, status) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (idempotency_key) DO NOTHING"
	UpdatePayment        = "UPDATE payment_log SET status = $1, payment_processor = $2, fee = $3, processing_started_at = NULL WHERE idempotency_key = $4 AND status = 'pending'"
	SelectFailedPayments = "SELECT idempotency_key, amount, fee, requested_at FROM payment_log WHERE status = 'failed' LIMIT 300 FOR UPDATE SKIP LOCKED"
	MarkPaymentPending   = "UPDATE payment_log SET status = 'pending', processing_started_at = NOW() WHERE idempotency_key = $1"
	StatsBoth            = "SELECT COALESCE(payment_processor, 'default') as processor, COUNT(*) as count, COALESCE(SUM(amount), 0) as total_amount FROM payment_log WHERE status = 'success' AND requested_at >= $1 AND requested_at <= $2 GROUP BY payment_processor"
	StatsFrom            = "SELECT COALESCE(payment_processor, 'default') as processor, COUNT(*) as count, COALESCE(SUM(amount), 0) as total_amount FROM payment_log WHERE status = 'success' AND requested_at >= $1 GROUP BY payment_processor"
	StatsTo              = "SELECT COALESCE(payment_processor, 'default') as processor, COUNT(*) as count, COALESCE(SUM(amount), 0) as total_amount FROM payment_log WHERE status = 'success' AND requested_at <= $1 GROUP BY payment_processor"
	StatsAll             = "SELECT COALESCE(payment_processor, 'default') as processor, COUNT(*) as count, COALESCE(SUM(amount), 0) as total_amount FROM payment_log WHERE status = 'success' GROUP BY payment_processor"
)

