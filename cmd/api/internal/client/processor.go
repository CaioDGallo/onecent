package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/types"
)

func GetProcessorFee(client *http.Client, processorURL string) (float64, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/admin/payments-summary", processorURL), nil)
	if err != nil {
		logger.Error("error creating request GetPPFee")
	}

	req.Header.Set("X-Rinha-Token", "123")

	resp, err := client.Do(req)
	if err != nil {
		logger.Error("error doing request GetPPFee")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("request failed with status")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("error reading body GetPPFee")
	}

	var summary types.AdminTransactionSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		logger.Error("error unmarshaling request GetPPFee")
	}

	floatFee, exact := summary.FeePerTransaction.Float64()
	if !exact {
		logger.Error("fee conversion not exact to float64 GetPPFee")
	}

	return floatFee, nil
}

