package store

import (
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

type PaymentRecord struct {
	CorrelationID     string          `json:"correlationId"`
	Amount            decimal.Decimal `json:"amount"`
	Fee               float64         `json:"fee"`
	PaymentProcessor  string          `json:"paymentProcessor"`
	RequestedAt       time.Time       `json:"requestedAt"`
	Status            string          `json:"status"`
}

type PaymentStore struct {
	mu       sync.RWMutex
	payments map[string]*PaymentRecord
	timeIndex []timeIndexEntry
}

type timeIndexEntry struct {
	timestamp     time.Time
	correlationID string
}

func NewPaymentStore() *PaymentStore {
	return &PaymentStore{
		payments:  make(map[string]*PaymentRecord),
		timeIndex: make([]timeIndexEntry, 0),
	}
}

func (ps *PaymentStore) StorePayment(correlationID string, amount decimal.Decimal, fee float64, processor string, requestedAt time.Time, status string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if _, exists := ps.payments[correlationID]; exists {
		return
	}

	payment := &PaymentRecord{
		CorrelationID:    correlationID,
		Amount:           amount,
		Fee:              fee,
		PaymentProcessor: processor,
		RequestedAt:      requestedAt,
		Status:           status,
	}

	ps.payments[correlationID] = payment

	ps.timeIndex = append(ps.timeIndex, timeIndexEntry{
		timestamp:     requestedAt,
		correlationID: correlationID,
	})

	sort.Slice(ps.timeIndex, func(i, j int) bool {
		return ps.timeIndex[i].timestamp.Before(ps.timeIndex[j].timestamp)
	})
}

func (ps *PaymentStore) UpdatePaymentStatus(correlationID string, status string, processor string, fee float64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if payment, exists := ps.payments[correlationID]; exists {
		payment.Status = status
		payment.PaymentProcessor = processor
		payment.Fee = fee
	}
}

func (ps *PaymentStore) GetPaymentStats(fromTime, toTime *time.Time) map[string]PaymentProcessorStats {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	stats := make(map[string]PaymentProcessorStats)

	for _, payment := range ps.payments {
		if payment.Status != "success" {
			continue
		}

		if fromTime != nil && payment.RequestedAt.Before(*fromTime) {
			continue
		}
		if toTime != nil && payment.RequestedAt.After(*toTime) {
			continue
		}

		processor := payment.PaymentProcessor
		if processor == "" {
			processor = "default"
		}

		existing := stats[processor]
		existing.TotalRequests++
		existing.TotalAmount = existing.TotalAmount.Add(payment.Amount)
		stats[processor] = existing
	}

	return stats
}

func (ps *PaymentStore) GetAllPayments() []*PaymentRecord {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	payments := make([]*PaymentRecord, 0, len(ps.payments))
	for _, payment := range ps.payments {
		paymentCopy := *payment
		payments = append(payments, &paymentCopy)
	}

	return payments
}

func (ps *PaymentStore) GetPaymentsInTimeRange(fromTime, toTime *time.Time) []*PaymentRecord {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	var result []*PaymentRecord

	for _, entry := range ps.timeIndex {
		if fromTime != nil && entry.timestamp.Before(*fromTime) {
			continue
		}
		if toTime != nil && entry.timestamp.After(*toTime) {
			break
		}

		if payment, exists := ps.payments[entry.correlationID]; exists {
			paymentCopy := *payment
			result = append(result, &paymentCopy)
		}
	}

	return result
}

type PaymentProcessorStats struct {
	TotalRequests int             `json:"totalRequests"`
	TotalAmount   decimal.Decimal `json:"totalAmount"`
}