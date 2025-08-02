package handlers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/store"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

type StatsHandler struct {
	StatsAggregator *store.StatsAggregator
}

func NewStatsHandler(statsAggregator *store.StatsAggregator) *StatsHandler {
	return &StatsHandler{
		StatsAggregator: statsAggregator,
	}
}

func (h *StatsHandler) GetPaymentsSummary(c fiber.Ctx) error {
	fromParam := c.Query("from")
	toParam := c.Query("to")

	var fromTime, toTime *time.Time
	var err error

	if fromParam != "" {
		parsedTime, err := time.Parse("2006-01-02T15:04:05.000Z", fromParam)
		if err != nil {
			logger.Error("failed parsing from parameter")
			return c.Status(400).Send([]byte("Invalid 'from' parameter format"))
		}
		fromTime = &parsedTime
	}

	if toParam != "" {
		parsedTime, err := time.Parse("2006-01-02T15:04:05.000Z", toParam)
		if err != nil {
			logger.Error("failed parsing to parameter")
			return c.Status(400).Send([]byte("Invalid 'to' parameter format"))
		}
		toTime = &parsedTime
	}

	aggregatedStats := h.StatsAggregator.GetAggregatedStats(fromTime, toTime)

	defaultStats := aggregatedStats["default"]
	fallbackStats := aggregatedStats["fallback"]

	paymentsSummaryResp := &types.PaymentsSummaryResponse{
		Default:  defaultStats,
		Fallback: fallbackStats,
	}

	resp, err := json.Marshal(paymentsSummaryResp)
	if err != nil {
		logger.Error("failed marshaling response")
		return c.Status(500).Send([]byte("Internal server error"))
	}

	return c.Send(resp)
}
