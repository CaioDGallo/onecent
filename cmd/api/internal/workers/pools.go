package workers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/sony/gobreaker/v2"

	"github.com/CaioDGallo/onecent/cmd/api/internal/config"
	"github.com/CaioDGallo/onecent/cmd/api/internal/database"
	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/metrics"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

type WorkerPools struct {
	PaymentTaskChannel      chan types.PaymentTask
	RetryTaskChannel        chan types.PaymentTask
	DB                      *sql.DB
	PreparedStmts           *database.PreparedStatements
	DefaultEndpoint         string
	FallbackEndpoint        string
	DefaultFee              float64
	FallbackFee             float64
	Client                  *http.Client
	DefaultCircuitBreaker   *gobreaker.CircuitBreaker[[]byte]
	FallbackCircuitBreaker  *gobreaker.CircuitBreaker[[]byte]
	RetryCircuitBreaker     *gobreaker.CircuitBreaker[[]byte]
	wg                      sync.WaitGroup
	ctx                     context.Context
	cancel                  context.CancelFunc
	lastDefaultState        gobreaker.State
	lastFallbackState       gobreaker.State
	stateMutex              sync.RWMutex
	defaultHealth           types.ProcessorHealth
	fallbackHealth          types.ProcessorHealth
	healthMutex             sync.RWMutex
	lastDefaultHealthCheck  time.Time
	lastFallbackHealthCheck time.Time
	otherInstanceURL        string
	paymentPool             *ants.Pool
	retryPool               *ants.Pool
}

func NewWorkerPools(db *sql.DB, preparedStmts *database.PreparedStatements, defaultEndpoint, fallbackEndpoint string, defaultFee, fallbackFee float64, httpClient *http.Client, defaultCB, fallbackCB, retryCB *gobreaker.CircuitBreaker[[]byte]) (*WorkerPools, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create ants pools
	paymentPoolSize := config.GetPaymentPoolSize()
	retryPoolSize := config.GetRetryPoolSize()

	paymentPool, err := ants.NewPool(paymentPoolSize, ants.WithNonblocking(false))
	if err != nil {
		cancel()
		return nil, err
	}

	retryPool, err := ants.NewPool(retryPoolSize, ants.WithNonblocking(false))
	if err != nil {
		paymentPool.Release()
		cancel()
		return nil, err
	}

	wp := &WorkerPools{
		PaymentTaskChannel:     make(chan types.PaymentTask, 2500),
		RetryTaskChannel:       make(chan types.PaymentTask, 3000),
		DB:                     db,
		PreparedStmts:          preparedStmts,
		DefaultEndpoint:        defaultEndpoint,
		FallbackEndpoint:       fallbackEndpoint,
		DefaultFee:             defaultFee,
		FallbackFee:            fallbackFee,
		Client:                 httpClient,
		DefaultCircuitBreaker:  defaultCB,
		FallbackCircuitBreaker: fallbackCB,
		RetryCircuitBreaker:    retryCB,
		ctx:                    ctx,
		cancel:                 cancel,
		otherInstanceURL:       config.GetOtherInstanceURL(),
		paymentPool:            paymentPool,
		retryPool:              retryPool,
	}

	return wp, nil
}

func (wp *WorkerPools) SetCircuitBreakers(defaultCB, fallbackCB, retryCB *gobreaker.CircuitBreaker[[]byte]) {
	wp.DefaultCircuitBreaker = defaultCB
	wp.FallbackCircuitBreaker = fallbackCB
	wp.RetryCircuitBreaker = retryCB
	wp.lastDefaultState = defaultCB.State()
	wp.lastFallbackState = fallbackCB.State()
}

func (wp *WorkerPools) TriggerRetries() {
	// if wp.DefaultCircuitBreaker == nil || wp.FallbackCircuitBreaker == nil {
	// 	return
	// }
	// logger.Info("Circuit breaker became available - triggering immediate retry burst")
	wp.processFailedPayments()
}

func (wp *WorkerPools) Shutdown(timeout time.Duration) error {
	logger.Info("Starting graceful shutdown of worker pools")

	wp.cancel()
	close(wp.PaymentTaskChannel)
	close(wp.RetryTaskChannel)

	done := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All worker tasks completed successfully")
	case <-time.After(timeout):
		logger.Info("Timeout reached, forcing shutdown")
	}

	// Release ants pools
	wp.paymentPool.Release()
	wp.retryPool.Release()

	logger.Info("Worker pools shutdown complete")
	return nil
}

func (wp *WorkerPools) StartPaymentConsumers() {
	// Single goroutine to consume from channel and submit to ants pool
	go func() {
		for {
			select {
			case <-wp.ctx.Done():
				return
			case task, ok := <-wp.PaymentTaskChannel:
				if !ok {
					return
				}

				// Submit task to ants pool
				wp.wg.Add(1)
				_ = wp.paymentPool.Submit(func() {
					defer wp.wg.Done()
					start := time.Now()

					// Record channel processing metrics
					metrics.RecordChannelProcess("payment")
					metrics.UpdateChannelSize("payment", len(wp.PaymentTaskChannel))

					// Update ants pool metrics
					metrics.UpdateAntsPoolMetrics("payment", wp.paymentPool.Running(), wp.paymentPool.Cap(), wp.paymentPool.Free())

					wp.ProcessPaymentDirect(task, false)

					// Record processing duration
					duration := time.Since(start).Seconds()
					metrics.RecordChannelProcessDuration("payment", duration)
				})
			}
		}
	}()
}

func (wp *WorkerPools) StartRetryConsumers() {
	// // Single goroutine to consume from retry channel and submit to ants pool
	// go func() {
	// 	for {
	// 		select {
	// 		case <-wp.ctx.Done():
	// 			return
	// 		case task, ok := <-wp.RetryTaskChannel:
	// 			if !ok {
	// 				return
	// 			}
	//
	// 			// Submit retry task to ants pool
	// 			wp.wg.Add(1)
	// 			_ = wp.retryPool.Submit(func() {
	// 				defer wp.wg.Done()
	//
	// 				// Update retry pool metrics
	// 				metrics.UpdateAntsPoolMetrics("retry", wp.retryPool.Running(), wp.retryPool.Cap(), wp.retryPool.Free())
	//
	// 				wp.ProcessPaymentDirect(task, true)
	// 			})
	// 		}
	// 	}
	// }()
	//
	// go func() {
	// 	ticker := time.NewTicker(1 * time.Second)
	// 	defer ticker.Stop()
	//
	// 	for {
	// 		select {
	// 		case <-wp.ctx.Done():
	// 			return
	// 		case <-ticker.C:
	// 			wp.TriggerRetries()
	// 		}
	// 	}
	// }()
}

func (wp *WorkerPools) updateProcessorHealth(processorType string, health types.ProcessorHealth) {
	wp.healthMutex.Lock()
	defer wp.healthMutex.Unlock()

	if processorType == "default" {
		wp.defaultHealth = health
	} else {
		wp.fallbackHealth = health
	}
}

func (wp *WorkerPools) getProcessorHealth(processorType string) types.ProcessorHealth {
	wp.healthMutex.RLock()
	defer wp.healthMutex.RUnlock()

	if processorType == "default" {
		return wp.defaultHealth
	}
	return wp.fallbackHealth
}

func (wp *WorkerPools) UpdateProcessorHealthExternal(processorType string, health types.ProcessorHealth) {
	wp.healthMutex.Lock()
	defer wp.healthMutex.Unlock()

	if processorType == "default" {
		wp.defaultHealth = health
	} else {
		wp.fallbackHealth = health
	}
}

type HealthSyncPayload struct {
	DefaultHealth  types.ProcessorHealth `json:"defaultHealth"`
	FallbackHealth types.ProcessorHealth `json:"fallbackHealth"`
}

func (wp *WorkerPools) syncHealthToOtherInstance() {
	if wp.otherInstanceURL == "" {
		return
	}

	wp.healthMutex.RLock()
	payload := HealthSyncPayload{
		DefaultHealth:  wp.defaultHealth,
		FallbackHealth: wp.fallbackHealth,
	}
	wp.healthMutex.RUnlock()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error("Failed to marshal health sync payload")
		return
	}

	req, err := http.NewRequest("POST", wp.otherInstanceURL+"/internal/health-sync", bytes.NewBuffer(payloadBytes))
	if err != nil {
		logger.Error("Failed to create health sync request")
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := wp.Client.Do(req)
	if err != nil {
		logger.Error("Failed to sync health to other instance")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		logger.Info("Health state synced to other instance successfully")
	} else {
		logger.Error("Health sync to other instance failed with status code")
	}
}

func (wp *WorkerPools) syncHealthAfterUpdate() {
	instanceID := config.GetInstanceID()
	if instanceID == "1" {
		go wp.syncHealthToOtherInstance()
	}
}

func (wp *WorkerPools) trackPaymentResult(processor string, success bool) {
	wp.healthMutex.Lock()
	defer wp.healthMutex.Unlock()

	var health *types.ProcessorHealth
	if processor == "default" {
		health = &wp.defaultHealth
	} else {
		health = &wp.fallbackHealth
	}

	if success {
		health.ConsecutiveSuccesses++
		health.ConsecutiveFailures = 0
	} else {
		health.ConsecutiveFailures++
		health.ConsecutiveSuccesses = 0
	}

	wp.checkHealthThresholds(health)
}

func (wp *WorkerPools) checkHealthThresholds(health *types.ProcessorHealth) {
	previouslyHealthy := health.IsValid && !health.Failing
	newHealthState := previouslyHealthy

	if health.ConsecutiveFailures >= 3 && previouslyHealthy {
		health.Failing = true
		health.IsValid = false
		newHealthState = false
		logger.Info("Processor marked as unhealthy due to 3 consecutive payment failures")
	} else if health.ConsecutiveSuccesses >= 3 && !previouslyHealthy {
		health.Failing = false
		health.IsValid = true
		newHealthState = true
		logger.Info("Processor marked as healthy due to 3 consecutive payment successes")
		wp.TriggerRetries()
	}

	if newHealthState != previouslyHealthy {
		health.LastChecked = time.Now()
		wp.syncHealthAfterUpdate()
	}
}
