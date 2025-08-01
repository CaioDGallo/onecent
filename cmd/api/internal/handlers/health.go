package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
	"github.com/CaioDGallo/onecent/cmd/api/internal/workers"
)

type HealthHandler struct {
	WorkerPools *workers.WorkerPools
}

type HealthSyncPayload struct {
	DefaultHealth  types.ProcessorHealth `json:"defaultHealth"`
	FallbackHealth types.ProcessorHealth `json:"fallbackHealth"`
}

func NewHealthHandler(workerPools *workers.WorkerPools) *HealthHandler {
	return &HealthHandler{
		WorkerPools: workerPools,
	}
}

func (h *HealthHandler) SyncHealth(c fiber.Ctx) error {
	var syncRequest HealthSyncPayload

	if err := json.Unmarshal(c.Body(), &syncRequest); err != nil {
		logger.Error("Failed to parse health sync request")
		return c.Status(400).JSON(fiber.Map{
			"error": "Invalid JSON",
		})
	}

	h.WorkerPools.UpdateProcessorHealthExternal("default", syncRequest.DefaultHealth)
	h.WorkerPools.UpdateProcessorHealthExternal("fallback", syncRequest.FallbackHealth)

	logger.Info("Health state synchronized from primary instance")

	return c.Status(200).JSON(fiber.Map{
		"status": "success",
	})
}
