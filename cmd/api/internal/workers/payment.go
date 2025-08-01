package workers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker/v2"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

func (wp *WorkerPools) ProcessPaymentDirect(task types.PaymentTask) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Payment processing worker panic recovered")
		}
	}()

	correlationID := task.Request.CorrelationID
	requestedAtString := task.RequestedAt.Format("2006-01-02T15:04:05.000Z")

	var fee float64
	processor := "default"
	fee = wp.DefaultFee

	ppPaymentRequest := &types.PaymentProcessorPaymentRequest{
		PaymentRequest: task.Request,
		RequestedAt:    requestedAtString,
	}

	ppPayload, err := json.Marshal(ppPaymentRequest)
	if err != nil {
		logger.Error("Error marshaling request")
		wp.retryPool.Submit(
			func() {
				time.Sleep(wp.calculateChannelBasedDelay())
				wp.PaymentTaskChannel <- task
			},
		)
		return
	}

	err = wp.executePaymentRequest(ppPayload)
	if err != nil {
		wp.retryPool.Submit(
			func() {
				time.Sleep(wp.calculateChannelBasedDelay())
				wp.PaymentTaskChannel <- task
			},
		)
		return
	}

	wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "success", false)
}

func (wp *WorkerPools) executePaymentRequest(ppPayload []byte) error {
	var endpoint string
	var circuitBreaker *gobreaker.CircuitBreaker[[]byte]

	endpoint = wp.DefaultEndpoint
	circuitBreaker = wp.DefaultCircuitBreaker

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/payments", endpoint), bytes.NewReader(ppPayload))
	if err != nil {
		return err
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

	return err
}

func (wp *WorkerPools) calculateChannelBasedDelay() time.Duration {
	channelLen := len(wp.PaymentTaskChannel)
	channelCap := cap(wp.PaymentTaskChannel)
	
	if channelCap == 0 {
		return 50 * time.Millisecond
	}
	
	fullnessRatio := float64(channelLen) / float64(channelCap)
	
	const minDelay = 10 * time.Millisecond
	const maxDelay = 500 * time.Millisecond
	
	baseDelay := time.Duration(float64(minDelay) + 
		fullnessRatio * float64(maxDelay - minDelay))
	
	jitterRange := baseDelay / 4
	jitter := time.Duration(rand.Intn(int(jitterRange*2))) - jitterRange
	
	return baseDelay + jitter
}

func (wp *WorkerPools) createPaymentRecord(correlationID string, amount decimal.Decimal, fee float64, processor string, requestedAt time.Time, status string, isRetry bool) {
	tx, err := wp.DB.Begin()
	if err != nil {
		logger.Error("CRITICAL: Failed to begin payment creation transaction")
		return
	}
	defer tx.Rollback()

	_, err = tx.Stmt(wp.PreparedStmts.Insert).Exec(correlationID, processor, amount, fee, requestedAt, status)
	if err != nil {
		logger.Error("CRITICAL: Failed to insert payment")
		return
	}

	if err = tx.Commit(); err != nil {
		logger.Error("CRITICAL: Failed to commit payment creation")
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
