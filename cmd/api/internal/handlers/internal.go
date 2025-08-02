package handlers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/store"
	"github.com/CaioDGallo/onecent/cmd/api/internal/workers"
)

type InternalHandler struct {
	WorkerPools     *workers.WorkerPools
	StatsAggregator *store.StatsAggregator
}

func NewInternalHandler(workerPools *workers.WorkerPools, statsAggregator *store.StatsAggregator) *InternalHandler {
	return &InternalHandler{
		WorkerPools:     workerPools,
		StatsAggregator: statsAggregator,
	}
}

type InternalStatsRequest struct {
	FromTime *time.Time `json:"fromTime"`
	ToTime   *time.Time `json:"toTime"`
}

type InternalStatsResponse struct {
	Stats map[string]store.PaymentProcessorStats `json:"stats"`
}

func (h *InternalHandler) GetLocalStats(c fiber.Ctx) error {
	var request InternalStatsRequest

	if err := json.Unmarshal(c.Body(), &request); err != nil {
		logger.Error("Failed to unmarshal internal stats request")
		return c.Status(400).JSON(fiber.Map{
			"error": "Invalid request format",
		})
	}

	stats := h.StatsAggregator.GetLocalStats(request.FromTime, request.ToTime)

	response := InternalStatsResponse{
		Stats: stats,
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		logger.Error("Failed to marshal internal stats response")
		return c.Status(500).JSON(fiber.Map{
			"error": "Internal server error",
		})
	}

	return c.Send(responseBytes)
}

func (h *InternalHandler) SyncHealth(c fiber.Ctx) error {
	var payload workers.HealthSyncPayload

	if err := json.Unmarshal(c.Body(), &payload); err != nil {
		logger.Error("Failed to unmarshal health sync payload")
		return c.Status(400).JSON(fiber.Map{
			"error": "Invalid payload format",
		})
	}

	h.WorkerPools.UpdateProcessorHealthExternal("default", payload.DefaultHealth)
	h.WorkerPools.UpdateProcessorHealthExternal("fallback", payload.FallbackHealth)

	logger.Info("Health state synchronized from other instance")

	return c.Status(200).Send(nil)
}

type RetryCoordinationRequest struct {
	CorrelationIDs []string `json:"correlationIds"`
	InstanceID     string   `json:"instanceId"`
}

func (h *InternalHandler) RequestRetryOwnership(c fiber.Ctx) error {
	var request RetryCoordinationRequest

	if err := json.Unmarshal(c.Body(), &request); err != nil {
		logger.Error("Failed to unmarshal retry coordination request")
		return c.Status(400).JSON(fiber.Map{
			"error": "Invalid request format",
		})
	}

	for _, correlationID := range request.CorrelationIDs {
		h.WorkerPools.RetryStore.SetInstanceOwnership(correlationID, request.InstanceID)
	}

	logger.Info("Retry ownership transferred for coordinated retries")

	return c.Status(200).Send(nil)
}