package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/sony/gobreaker/v2"

	"github.com/CaioDGallo/onecent/cmd/api/internal/config"
	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/store"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

type WorkerPools struct {
	PaymentTaskChannel      chan types.PaymentTask
	RetryTaskChannel        chan types.PaymentTask
	PaymentStore            *store.PaymentStore
	RetryStore              *store.RetryStore
	StatsAggregator         *store.StatsAggregator
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

func NewWorkerPools(defaultEndpoint, fallbackEndpoint string, defaultFee, fallbackFee float64, httpClient *http.Client, defaultCB, fallbackCB, retryCB *gobreaker.CircuitBreaker[[]byte]) (*WorkerPools, error) {
	ctx, cancel := context.WithCancel(context.Background())

	paymentPoolSize := config.GetPaymentPoolSize()
	retryPoolSize := config.GetRetryPoolSize()

	paymentPool, err := ants.NewPool(
		paymentPoolSize,
		ants.WithNonblocking(false),
		ants.WithPreAlloc(true),
	)
	if err != nil {
		cancel()
		return nil, err
	}

	retryPool, err := ants.NewPool(
		retryPoolSize,
		ants.WithNonblocking(false),
		ants.WithPreAlloc(true),
	)
	if err != nil {
		paymentPool.Release()
		cancel()
		return nil, err
	}

	instanceID := config.GetInstanceID()
	paymentStore := store.NewPaymentStore()
	retryStore := store.NewRetryStore(instanceID)
	statsAggregator := store.NewStatsAggregator(paymentStore, config.GetOtherInstanceURL(), httpClient, instanceID)

	wp := &WorkerPools{
		PaymentTaskChannel:     make(chan types.PaymentTask, 2500),
		RetryTaskChannel:       make(chan types.PaymentTask, 3000),
		PaymentStore:           paymentStore,
		RetryStore:             retryStore,
		StatsAggregator:        statsAggregator,
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

func (wp *WorkerPools) SetCircuitBreakers(defaultCB *gobreaker.CircuitBreaker[[]byte]) {
	wp.DefaultCircuitBreaker = defaultCB
	wp.lastDefaultState = defaultCB.State()
}

func (wp *WorkerPools) TriggerRetries() {
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

	wp.paymentPool.Release()
	wp.retryPool.Release()

	logger.Info("Worker pools shutdown complete")
	return nil
}

func (wp *WorkerPools) StartPaymentConsumers() {
	go func() {
		for {
			select {
			case <-wp.ctx.Done():
				return
			case task, ok := <-wp.PaymentTaskChannel:
				if !ok {
					return
				}

				wp.wg.Add(1)
				_ = wp.paymentPool.Submit(func() {
					defer wp.wg.Done()

					wp.ProcessPaymentDirect(task)
				})
			}
		}
	}()
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
