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
		wp.createPaymentRecord(correlationID, task.Request.Amount, fee, processor, task.RequestedAt, "failed", false)
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
	wp.PaymentStore.StorePayment(correlationID, amount, fee, processor, requestedAt, status)
	
	if status == "success" {
		wp.RetryStore.RemoveSuccessfulPayment(correlationID)
	} else if status == "failed" {
		wp.RetryStore.AddFailedPayment(correlationID, amount, fee, requestedAt)
	}
}

func (wp *WorkerPools) markMultiplePaymentsPending(correlationIDs []string) {
	if len(correlationIDs) == 0 {
		return
	}

	for _, id := range correlationIDs {
		wp.RetryStore.MarkPaymentPending(id)
	}
}

func (wp *WorkerPools) processFailedPayments() {
	wp.RetryStore.CleanupStaleLocks(5 * time.Minute)
	
	paymentTasks := wp.RetryStore.GetFailedPaymentsForRetry(300)

	if len(paymentTasks) == 0 {
		return
	}

	var paymentIDs []string
	for _, task := range paymentTasks {
		paymentIDs = append(paymentIDs, task.Request.CorrelationID)
	}

	wp.markMultiplePaymentsPending(paymentIDs)

	for _, task := range paymentTasks {
		wp.RetryTaskChannel <- task
	}
}
