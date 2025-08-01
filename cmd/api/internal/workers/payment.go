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

	processor := wp.determineHealthyProcessor()
	// if processor == "" {
	// 	logger.Error("No healthy processors available")
	// 	wp.createPaymentRecord(correlationID, task.Request.Amount, 0, "none", task.RequestedAt, "failed", isRetry)
	// 	return
	// }

	var fee float64
	if processor == "default" {
		fee = wp.DefaultFee
	} else {
		fee = wp.FallbackFee
	}

	ppPaymentRequest := &types.PaymentProcessorPaymentRequest{
		PaymentRequest: task.Request,
		RequestedAt:    requestedAtString,
	}

	ppPayload, err := json.Marshal(ppPaymentRequest)
	if err != nil {
		logger.Error("Error marshaling request")
		// wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", isRetry)
		if float64(len(wp.PaymentTaskChannel)/cap(wp.PaymentTaskChannel)) > float64(0.8) {
			logger.Error("HIGH FLOW: len: " + strconv.Itoa(len(wp.PaymentTaskChannel)) + " cap: " + strconv.Itoa(cap(wp.PaymentTaskChannel)))
		}
		logger.Error("FLOW: len: " + strconv.Itoa(len(wp.PaymentTaskChannel)) + " cap: " + strconv.Itoa(cap(wp.PaymentTaskChannel)))
		wp.retryPool.Submit(
			func() {
				time.Sleep(200 * time.Millisecond)
				wp.PaymentTaskChannel <- task
			},
		)
		return
	}

	err = wp.executePaymentRequest(processor, ppPayload, isRetry)
	if err != nil {
		// logger.Error("Payment processing failed")
		// logger.Error("CB state: " + wp.DefaultCircuitBreaker.State().String())
		// wp.trackPaymentResult(processor, false)
		// wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", isRetry)
		if float64(len(wp.PaymentTaskChannel)/cap(wp.PaymentTaskChannel)) > float64(0.8) {
			logger.Error("HIGH FLOW: len: " + strconv.Itoa(len(wp.PaymentTaskChannel)) + " cap: " + strconv.Itoa(cap(wp.PaymentTaskChannel)))
		}
		logger.Error("FLOW: len: " + strconv.Itoa(len(wp.PaymentTaskChannel)) + " cap: " + strconv.Itoa(cap(wp.PaymentTaskChannel)))
		wp.retryPool.Submit(
			func() {
				time.Sleep(150 * time.Millisecond)
				wp.PaymentTaskChannel <- task
			},
		)
		return
	}

	// wp.trackPaymentResult(processor, true)
	wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "success", isRetry)
}

func (wp *WorkerPools) determineHealthyProcessor() string {
	// defaultHealth := wp.getProcessorHealth("default")
	// fallbackHealth := wp.getProcessorHealth("fallback")
	//
	// defaultHealthy := (defaultHealth.IsValid && !defaultHealth.Failing) && wp.DefaultCircuitBreaker.State() == gobreaker.StateClosed
	// fallbackHealthy := (fallbackHealth.IsValid && !fallbackHealth.Failing) && wp.FallbackCircuitBreaker.State() == gobreaker.StateClosed
	//
	// if defaultHealthy {
	// 	return "default"
	// }
	// if fallbackHealthy {
	// 	return "fallback"
	// }

	return "default"
}

func (wp *WorkerPools) executePaymentRequest(processor string, ppPayload []byte, isRetry bool) error {
	var endpoint string
	var circuitBreaker *gobreaker.CircuitBreaker[[]byte]

	if processor == "default" {
		endpoint = wp.DefaultEndpoint
		circuitBreaker = wp.DefaultCircuitBreaker
	} else {
		endpoint = wp.FallbackEndpoint
		circuitBreaker = wp.FallbackCircuitBreaker
	}

	// if isRetry {
	// 	circuitBreaker = wp.RetryCircuitBreaker
	// }

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/payments", endpoint), bytes.NewReader(ppPayload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", strconv.Itoa(len(ppPayload)))

	// resp, err := wp.Client.Do(req)
	// if err != nil {
	// 	return err
	// }
	// defer resp.Body.Close()
	//
	// if resp.StatusCode >= 400 {
	// 	return fmt.Errorf("downstream error: %d", resp.StatusCode)
	// }
	//
	// return nil

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

	return err
}

func (wp *WorkerPools) createPaymentRecord(correlationID string, amount decimal.Decimal, fee float64, processor string, requestedAt time.Time, status string, isRetry bool) {
	tx, err := wp.DB.Begin()
	if err != nil {
		logger.Error("CRITICAL: Failed to begin payment creation transaction")
		return
	}
	defer tx.Rollback()

	if isRetry {
		_, err = tx.Stmt(wp.PreparedStmts.Update).Exec(status, processor, fee, correlationID)
		if err != nil {
			logger.Error("CRITICAL: Failed to update payment")
			return
		}
	} else {
		_, err = tx.Stmt(wp.PreparedStmts.Insert).Exec(correlationID, processor, amount, fee, requestedAt, status)
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

func (wp *WorkerPools) markPaymentPending(correlationID string) {
	tx, err := wp.DB.Begin()
	if err != nil {
		logger.Error("Failed to begin payment pending transaction")
		return
	}
	defer tx.Rollback()

	_, err = tx.Stmt(wp.PreparedStmts.MarkPending).Exec(correlationID)
	if err != nil {
		logger.Error("Failed to mark payment as pending")
		return
	}

	if err = tx.Commit(); err != nil {
		logger.Error("Failed to commit payment pending transaction")
		return
	}
}

func (wp *WorkerPools) markMultiplePaymentsPending(correlationIDs []string) {
	if len(correlationIDs) == 0 {
		return
	}

	tx, err := wp.DB.Begin()
	if err != nil {
		logger.Error("Failed to begin batch payment pending transaction")
		return
	}
	defer tx.Rollback()

	for _, id := range correlationIDs {
		_, err = tx.Stmt(wp.PreparedStmts.MarkPending).Exec(id)
		if err != nil {
			logger.Error("Failed to mark payment as pending in batch")
			return
		}
	}

	if err = tx.Commit(); err != nil {
		logger.Error("Failed to commit batch payment pending transaction")
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

	rows, err := tx.Stmt(wp.PreparedStmts.SelectFailed).Query()
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

	if err = tx.Commit(); err != nil {
		logger.Error("Failed to commit retry transaction")
		return
	}

	if len(paymentIDs) == 0 {
		return
	}

	wp.markMultiplePaymentsPending(paymentIDs)

	for _, task := range paymentTasks {
		wp.RetryTaskChannel <- task
	}
}
