package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

type StatsAggregator struct {
	paymentStore     *PaymentStore
	otherInstanceURL string
	httpClient       *http.Client
	instanceID       string
}

type StatsRequest struct {
	FromTime *time.Time `json:"fromTime"`
	ToTime   *time.Time `json:"toTime"`
}

type StatsResponse struct {
	Stats map[string]PaymentProcessorStats `json:"stats"`
}

func NewStatsAggregator(paymentStore *PaymentStore, otherInstanceURL string, httpClient *http.Client, instanceID string) *StatsAggregator {
	return &StatsAggregator{
		paymentStore:     paymentStore,
		otherInstanceURL: otherInstanceURL,
		httpClient:       httpClient,
		instanceID:       instanceID,
	}
}

func (sa *StatsAggregator) GetAggregatedStats(fromTime, toTime *time.Time) map[string]types.PaymentProcessorStats {
	localStats := sa.paymentStore.GetPaymentStats(fromTime, toTime)
	
	remoteStats := sa.getRemoteStats(fromTime, toTime)
	
	return sa.mergeStats(localStats, remoteStats)
}

func (sa *StatsAggregator) getRemoteStats(fromTime, toTime *time.Time) map[string]PaymentProcessorStats {
	if sa.otherInstanceURL == "" {
		return make(map[string]PaymentProcessorStats)
	}

	request := StatsRequest{
		FromTime: fromTime,
		ToTime:   toTime,
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		logger.Error("Failed to marshal stats request")
		return make(map[string]PaymentProcessorStats)
	}

	req, err := http.NewRequest("POST", sa.otherInstanceURL+"/internal/stats", bytes.NewBuffer(requestBody))
	if err != nil {
		logger.Error("Failed to create stats request")
		return make(map[string]PaymentProcessorStats)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := sa.httpClient.Do(req)
	if err != nil {
		logger.Error("Failed to get stats from other instance")
		return make(map[string]PaymentProcessorStats)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Error(fmt.Sprintf("Stats request failed with status: %d", resp.StatusCode))
		return make(map[string]PaymentProcessorStats)
	}

	var statsResponse StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&statsResponse); err != nil {
		logger.Error("Failed to decode stats response")
		return make(map[string]PaymentProcessorStats)
	}

	return statsResponse.Stats
}

func (sa *StatsAggregator) mergeStats(local, remote map[string]PaymentProcessorStats) map[string]types.PaymentProcessorStats {
	merged := make(map[string]types.PaymentProcessorStats)

	allProcessors := make(map[string]bool)
	for processor := range local {
		allProcessors[processor] = true
	}
	for processor := range remote {
		allProcessors[processor] = true
	}

	for processor := range allProcessors {
		localStat := local[processor]
		remoteStat := remote[processor]

		mergedStat := types.PaymentProcessorStats{
			TotalRequests: localStat.TotalRequests + remoteStat.TotalRequests,
			TotalAmount:   types.NewJSONDecimal(localStat.TotalAmount.Add(remoteStat.TotalAmount)),
		}

		merged[processor] = mergedStat
	}

	if _, exists := merged["default"]; !exists {
		merged["default"] = types.PaymentProcessorStats{
			TotalRequests: 0,
			TotalAmount:   types.NewJSONDecimal(decimal.NewFromInt(0)),
		}
	}

	if _, exists := merged["fallback"]; !exists {
		merged["fallback"] = types.PaymentProcessorStats{
			TotalRequests: 0,
			TotalAmount:   types.NewJSONDecimal(decimal.NewFromInt(0)),
		}
	}

	return merged
}

func (sa *StatsAggregator) GetLocalStats(fromTime, toTime *time.Time) map[string]PaymentProcessorStats {
	return sa.paymentStore.GetPaymentStats(fromTime, toTime)
}