package workers

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/CaioDGallo/onecent/cmd/api/internal/database"
	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
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
}

func NewWorkerPools(db *sql.DB, preparedStmts *database.PreparedStatements, defaultEndpoint, fallbackEndpoint string, defaultFee, fallbackFee float64, httpClient *http.Client, defaultCB, fallbackCB, retryCB *gobreaker.CircuitBreaker[[]byte]) (*WorkerPools, error) {
	ctx, cancel := context.WithCancel(context.Background())

	wp := &WorkerPools{
		PaymentTaskChannel:     make(chan types.PaymentTask, 2500),
		RetryTaskChannel:       make(chan types.PaymentTask, 2000),
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
	if wp.DefaultCircuitBreaker == nil || wp.FallbackCircuitBreaker == nil {
		return
	}
	logger.Info("Circuit breaker became available - triggering immediate retry burst")
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

	logger.Info("Worker pools shutdown complete")
	return nil
}

func (wp *WorkerPools) StartPaymentConsumers() {
	for range 150 {
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
					func() {
						defer wp.wg.Done()
						wp.ProcessPaymentDirect(task, false)
					}()
				}
			}
		}()
	}
}

func (wp *WorkerPools) StartRetryConsumers() {
	for range 0 {
		go func() {
			for {
				select {
				case <-wp.ctx.Done():
					return
				case task, ok := <-wp.RetryTaskChannel:
					if !ok {
						return
					}
					wp.wg.Add(1)
					func() {
						defer wp.wg.Done()
						wp.ProcessPaymentDirect(task, true)
					}()
				}
			}
		}()
	}
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
