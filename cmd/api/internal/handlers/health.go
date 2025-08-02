package handlers

import (
	"github.com/CaioDGallo/onecent/cmd/api/internal/workers"
)

type HealthHandler struct {
	WorkerPools *workers.WorkerPools
}

func NewHealthHandler(workerPools *workers.WorkerPools) *HealthHandler {
	return &HealthHandler{
		WorkerPools: workerPools,
	}
}
