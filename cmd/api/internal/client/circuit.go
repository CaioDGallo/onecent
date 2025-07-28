package client

import (
	"time"

	"github.com/sony/gobreaker/v2"
)

func NewCircuitBreakers(onRetryTrigger func()) (*gobreaker.CircuitBreaker[[]byte], *gobreaker.CircuitBreaker[[]byte], *gobreaker.CircuitBreaker[[]byte]) {
	var defaultSettings gobreaker.Settings
	defaultSettings.Name = "Default Payments Breaker"
	defaultSettings.MaxRequests = 10
	defaultSettings.Interval = time.Duration(1 * time.Second)
	defaultSettings.Timeout = time.Duration(1500 * time.Millisecond)
	defaultSettings.ReadyToTrip = func(counts gobreaker.Counts) bool {
		return counts.ConsecutiveFailures >= 5
	}
	defaultSettings.OnStateChange = func(name string, from gobreaker.State, to gobreaker.State) {
		if (from == gobreaker.StateOpen || from == gobreaker.StateHalfOpen) && to == gobreaker.StateClosed {
			if onRetryTrigger != nil {
				go onRetryTrigger()
			}
		}
	}

	defaultCB := gobreaker.NewCircuitBreaker[[]byte](defaultSettings)

	var fallbackSettings gobreaker.Settings
	fallbackSettings.Name = "Fallback Payments Breaker"
	fallbackSettings.MaxRequests = 10
	fallbackSettings.Interval = time.Duration(1 * time.Second)
	fallbackSettings.Timeout = time.Duration(1500 * time.Millisecond)
	fallbackSettings.ReadyToTrip = func(counts gobreaker.Counts) bool {
		return counts.ConsecutiveFailures >= 5
	}

	fallbackCB := gobreaker.NewCircuitBreaker[[]byte](fallbackSettings)

	var retrySettings gobreaker.Settings
	retrySettings.Name = "Retry Payments Breaker"
	retrySettings.MaxRequests = 5
	retrySettings.Interval = time.Duration(1 * time.Second)
	retrySettings.Timeout = time.Duration(1500 * time.Millisecond)

	retryCB := gobreaker.NewCircuitBreaker[[]byte](retrySettings)

	return defaultCB, fallbackCB, retryCB
}
