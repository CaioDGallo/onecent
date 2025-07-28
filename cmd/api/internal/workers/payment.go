package workers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker/v2"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

func (wp *WorkerPools) ProcessPaymentDirect(task types.PaymentTask, isRetry bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Payment processing worker panic recovered")
		}
	}()

	correlationID := task.Request.CorrelationID
	requestedAtString := task.RequestedAt.Format("2006-01-02T15:04:05.000Z")

	var processor string
	var fee float64

	defaultHealth := wp.getProcessorHealth("default")
	fallbackHealth := wp.getProcessorHealth("fallback")

	defaultHealthy := (defaultHealth.IsValid && !defaultHealth.Failing) && wp.DefaultCircuitBreaker.State() == gobreaker.StateClosed
	fallbackHealthy := (fallbackHealth.IsValid && !fallbackHealth.Failing) && wp.FallbackCircuitBreaker.State() == gobreaker.StateClosed

	if defaultHealthy && !fallbackHealthy {
		processor = "default"
		fee = wp.DefaultFee
	} else if !defaultHealthy && fallbackHealthy {
		processor = "fallback"
		fee = wp.FallbackFee
	} else if defaultHealthy && fallbackHealthy {
		processor = "default"
		fee = wp.DefaultFee
	} else {
		processor = "default"
		fee = wp.DefaultFee
	}

	ppPaymentRequest := &types.PaymentProcessorPaymentRequest{
		PaymentRequest: task.Request,
		RequestedAt:    requestedAtString,
	}

	ppPayload, err := json.Marshal(ppPaymentRequest)
	if err != nil {
		logger.Error("Error marshaling request")
		wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", isRetry)
		return
	}

	var endpoint string
	var circuitBreaker *gobreaker.CircuitBreaker[[]byte]

	if processor == "default" {
		endpoint = wp.DefaultEndpoint
		circuitBreaker = wp.DefaultCircuitBreaker
	} else {
		endpoint = wp.FallbackEndpoint
		circuitBreaker = wp.FallbackCircuitBreaker
	}

	if isRetry {
		circuitBreaker = wp.RetryCircuitBreaker
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/payments", endpoint), bytes.NewReader(ppPayload))
	if err != nil {
		logger.Error("Error creating request")
		wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", isRetry)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", strconv.Itoa(len(ppPayload)))

	_, err = circuitBreaker.Execute(func() ([]byte, error) {
		resp, err := wp.Client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("downstream error: %d", resp.StatusCode)
		}

		return []byte{}, nil
	})

	if err != nil && processor == "default" && (defaultHealthy && fallbackHealthy || (!defaultHealthy && !fallbackHealthy)) && wp.FallbackCircuitBreaker.State() == gobreaker.StateClosed {
		logger.Error("Default processor failed, trying fallback")

		req, err = http.NewRequest("POST", fmt.Sprintf("%s/payments", wp.FallbackEndpoint), bytes.NewReader(ppPayload))
		if err != nil {
			logger.Error("Error creating fallback request")
			wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", isRetry)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", strconv.Itoa(len(ppPayload)))

		circuitBreaker = wp.FallbackCircuitBreaker
		if isRetry {
			circuitBreaker = wp.RetryCircuitBreaker
		}
		_, err = circuitBreaker.Execute(func() ([]byte, error) {
			resp, err := wp.Client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				return nil, fmt.Errorf("downstream error: %d", resp.StatusCode)
			}

			return []byte{}, nil
		})

		if err == nil {
			processor = "fallback"
			fee = wp.FallbackFee
		}
	}

	if err != nil {
		logger.Error("Payment processing failed")
		wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", isRetry)
		return
	}

	wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "success", isRetry)
}

func (wp *WorkerPools) createPaymentRecord(correlationID string, amount decimal.Decimal, fee float64, processor string, requestedAt time.Time, status string, isRetry bool) {
	tx, err := wp.DB.Begin()
	if err != nil {
		logger.Error("CRITICAL: Failed to begin payment creation transaction")
		return
	}
	defer tx.Rollback()

	if isRetry {
		_, err = tx.Exec(
			"UPDATE payment_log SET status = $1, payment_processor = $2, fee = $3, processing_started_at = NULL WHERE idempotency_key = $4 AND status = 'pending'",
			status, processor, fee, correlationID,
		)
		if err != nil {
			logger.Error("CRITICAL: Failed to update payment")
			return
		}
	} else {
		_, err = tx.Exec(
			"INSERT INTO payment_log (idempotency_key, payment_processor, amount, fee, requested_at, status) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (idempotency_key) DO NOTHING",
			correlationID, processor, amount, fee, requestedAt, status,
		)
		if err != nil {
			logger.Error("CRITICAL: Failed to insert payment")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		logger.Error("CRITICAL: Failed to commit payment creation")
		return
	}
}

func (wp *WorkerPools) processFailedPayments() {
	tx, err := wp.DB.Begin()
	if err != nil {
		logger.Error("Failed to begin transaction for retry processing")
		return
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT idempotency_key, amount, fee, requested_at 
		FROM payment_log 
		WHERE status = 'failed' 
		AND (processing_started_at IS NULL OR processing_started_at < NOW() - INTERVAL '30 seconds')
		ORDER BY requested_at ASC 
		LIMIT 200 
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		logger.Error("Failed to query failed payments")
		return
	}
	defer rows.Close()

	var paymentIDs []string
	var paymentTasks []types.PaymentTask

	for rows.Next() {
		var correlationID string
		var amount decimal.Decimal
		var fee float64
		var requestedAt time.Time

		if err := rows.Scan(&correlationID, &amount, &fee, &requestedAt); err != nil {
			logger.Error("Failed to scan failed payment row")
			continue
		}

		paymentIDs = append(paymentIDs, correlationID)
		paymentTasks = append(paymentTasks, types.PaymentTask{
			Request: types.PaymentRequest{
				CorrelationID: correlationID,
				Amount:        amount,
			},
			RequestedAt: requestedAt,
			Fee:         fee,
		})
	}

	if len(paymentIDs) == 0 {
		return
	}

	for _, id := range paymentIDs {
		_, err = tx.Exec("UPDATE payment_log SET status = 'pending', processing_started_at = NOW() WHERE idempotency_key = $1", id)
		if err != nil {
			logger.Error("Failed to mark payment as pending")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		logger.Error("Failed to commit retry transaction")
		return
	}

	processedCount := 0
	for _, task := range paymentTasks {
		wp.wg.Add(1)
		if err := wp.RetryPool.Submit(func() {
			defer wp.wg.Done()
			wp.ProcessPaymentDirect(task, true)
		}); err != nil {
			wp.wg.Done()
			logger.Error("Failed to submit retry for payment")
		} else {
			processedCount++
		}
	}

	logger.Info("retrying debug")
}
