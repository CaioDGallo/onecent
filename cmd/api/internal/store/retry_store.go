package store

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

type RetryRecord struct {
	CorrelationID    string          `json:"correlationId"`
	Amount           decimal.Decimal `json:"amount"`
	Fee              float64         `json:"fee"`
	RequestedAt      time.Time       `json:"requestedAt"`
	RetryCount       int             `json:"retryCount"`
	LastRetryAt      time.Time       `json:"lastRetryAt"`
	ProcessingLocked bool            `json:"processingLocked"`
	InstanceOwner    string          `json:"instanceOwner"`
}

type RetryStore struct {
	mu            sync.RWMutex
	failedPayments map[string]*RetryRecord
	instanceID    string
}

func NewRetryStore(instanceID string) *RetryStore {
	return &RetryStore{
		failedPayments: make(map[string]*RetryRecord),
		instanceID:     instanceID,
	}
}

func (rs *RetryStore) AddFailedPayment(correlationID string, amount decimal.Decimal, fee float64, requestedAt time.Time) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	rs.failedPayments[correlationID] = &RetryRecord{
		CorrelationID:    correlationID,
		Amount:           amount,
		Fee:              fee,
		RequestedAt:      requestedAt,
		RetryCount:       0,
		LastRetryAt:      time.Time{},
		ProcessingLocked: false,
		InstanceOwner:    rs.instanceID,
	}
}

func (rs *RetryStore) GetFailedPaymentsForRetry(limit int) []types.PaymentTask {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	var tasks []types.PaymentTask
	count := 0

	for correlationID, record := range rs.failedPayments {
		if count >= limit {
			break
		}

		if record.ProcessingLocked {
			continue
		}

		if record.InstanceOwner != rs.instanceID && record.InstanceOwner != "" {
			continue
		}

		record.ProcessingLocked = true
		record.LastRetryAt = time.Now()
		record.RetryCount++
		record.InstanceOwner = rs.instanceID

		task := types.PaymentTask{
			Request: types.PaymentRequest{
				CorrelationID: correlationID,
				Amount:        record.Amount,
			},
			RequestedAt: record.RequestedAt,
			Fee:         record.Fee,
		}

		tasks = append(tasks, task)
		count++
	}

	return tasks
}

func (rs *RetryStore) MarkPaymentPending(correlationID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if record, exists := rs.failedPayments[correlationID]; exists {
		record.ProcessingLocked = true
		record.InstanceOwner = rs.instanceID
	}
}

func (rs *RetryStore) RemoveSuccessfulPayment(correlationID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	delete(rs.failedPayments, correlationID)
}

func (rs *RetryStore) UnlockPayment(correlationID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if record, exists := rs.failedPayments[correlationID]; exists {
		record.ProcessingLocked = false
	}
}

func (rs *RetryStore) GetRetryCount(correlationID string) int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	if record, exists := rs.failedPayments[correlationID]; exists {
		return record.RetryCount
	}
	return 0
}

func (rs *RetryStore) GetAllFailedPayments() []*RetryRecord {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	records := make([]*RetryRecord, 0, len(rs.failedPayments))
	for _, record := range rs.failedPayments {
		recordCopy := *record
		records = append(records, &recordCopy)
	}

	return records
}

func (rs *RetryStore) SetInstanceOwnership(correlationID string, instanceID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if record, exists := rs.failedPayments[correlationID]; exists {
		record.InstanceOwner = instanceID
	}
}

func (rs *RetryStore) CleanupStaleLocks(staleDuration time.Duration) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	now := time.Now()
	for _, record := range rs.failedPayments {
		if record.ProcessingLocked && 
		   !record.LastRetryAt.IsZero() && 
		   now.Sub(record.LastRetryAt) > staleDuration {
			record.ProcessingLocked = false
			record.InstanceOwner = ""
		}
	}
}