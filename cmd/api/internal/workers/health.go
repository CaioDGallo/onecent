package workers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

func (wp *WorkerPools) StartHealthCheckWorker() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-wp.ctx.Done():
				return
			case <-ticker.C:
				wp.checkProcessorHealth()
			}
		}
	}()
}

func (wp *WorkerPools) checkProcessorHealth() {
	now := time.Now()

	if now.Sub(wp.lastDefaultHealthCheck) >= 5*time.Second {
		go wp.checkSingleProcessorHealth(wp.DefaultEndpoint, "default")
		wp.lastDefaultHealthCheck = now
	}

	if now.Sub(wp.lastFallbackHealthCheck) >= 5*time.Second {
		time.Sleep(2500 * time.Millisecond)
		go wp.checkSingleProcessorHealth(wp.FallbackEndpoint, "fallback")
		wp.lastFallbackHealthCheck = now.Add(2500 * time.Millisecond)
	}
}

func (wp *WorkerPools) checkSingleProcessorHealth(endpoint, processorType string) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/payments/service-health", endpoint), nil)
	if err != nil {
		logger.Error("Error creating health check request")
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := wp.Client.Do(req)
	if err != nil {
		logger.Error("Health check failed for processor")
		wp.updateProcessorHealth(processorType, types.ProcessorHealth{IsValid: false})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		logger.Error("Health check rate limited for processor")
		return
	}

	if resp.StatusCode != 200 {
		logger.Error("Health check returned bad status for processor")
		wp.updateProcessorHealth(processorType, types.ProcessorHealth{IsValid: false})
		return
	}

	var health types.ProcessorHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		logger.Error("Error decoding health response for processor")
		wp.updateProcessorHealth(processorType, types.ProcessorHealth{IsValid: false})
		return
	}

	health.LastChecked = time.Now()
	health.IsValid = true

	logger.Info("HEALTHCHECK SUCCESSFUL")

	wp.updateProcessorHealth(processorType, health)
	logger.Info("Processor became healthy - triggering priority retry burst")
	// go func() {
	// 	wp.TriggerRetries()
	// }()
}
