package client

import (
	"time"

	"github.com/sony/gobreaker/v2"
)

func NewCircuitBreaker() *gobreaker.CircuitBreaker[[]byte] {
	var defaultSettings gobreaker.Settings
	defaultSettings.Name = "Default Payments Breaker"
	defaultSettings.MaxRequests = 3
	defaultSettings.Timeout = time.Duration(100 * time.Millisecond)
	defaultSettings.ReadyToTrip = func(counts gobreaker.Counts) bool {
		return counts.ConsecutiveFailures >= 3
	}

	defaultCB := gobreaker.NewCircuitBreaker[[]byte](defaultSettings)

	return defaultCB
}
