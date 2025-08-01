package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CaioDGallo/onecent/cmd/api/internal/client"
	"github.com/CaioDGallo/onecent/cmd/api/internal/config"
	"github.com/CaioDGallo/onecent/cmd/api/internal/database"
	"github.com/CaioDGallo/onecent/cmd/api/internal/handlers"
	"github.com/CaioDGallo/onecent/cmd/api/internal/logger"
	"github.com/CaioDGallo/onecent/cmd/api/internal/router"
	"github.com/CaioDGallo/onecent/cmd/api/internal/workers"
)

func main() {
	db, preparedStmts, err := database.SetupDatabase(config.GetDatabaseConnectionString())
	if err != nil {
		logger.Fatal("failed to setup database")
	}
	defer db.Close()
	defer preparedStmts.Close()

	httpClient := client.NewHTTPClient()

	defaultFee, err := client.GetProcessorFee(httpClient, config.GetDefaultProcessorEndpoint())
	if err != nil {
		logger.Fatal("failed getting the default processor transaction fee")
	}

	fallbackFee, err := client.GetProcessorFee(httpClient, config.GetFallbackProcessorEndpoint())
	if err != nil {
		logger.Fatal("failed getting the fallback processor transaction fee")
	}

	workerPools, err := workers.NewWorkerPools(db, preparedStmts, config.GetDefaultProcessorEndpoint(), config.GetFallbackProcessorEndpoint(), defaultFee, fallbackFee, httpClient, nil, nil, nil)
	if err != nil {
		logger.Fatal("failed to initialize worker pools")
	}

	defaultCB, fallbackCB, retryCB := client.NewCircuitBreakers(workerPools.TriggerRetries)

	workerPools.SetCircuitBreakers(defaultCB, fallbackCB, retryCB)

	workerPools.StartHealthCheckWorker()
	workerPools.StartPaymentConsumers()
	workerPools.StartRetryConsumers()

	paymentHandler := handlers.NewPaymentHandler(workerPools, defaultFee)
	statsHandler := handlers.NewStatsHandler(preparedStmts)
	healthHandler := handlers.NewHealthHandler(workerPools)

	app := router.SetupRoutes(paymentHandler, statsHandler, healthHandler)

	go func() {
		if err := app.Listen(config.ServerAddress); err != nil {
			logger.Error("Server failed to start")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server")

	if err := app.Shutdown(); err != nil {
		logger.Error("Server forced to shutdown")
	}

	if err := workerPools.Shutdown(30 * time.Second); err != nil {
		logger.Error("Worker pools shutdown failed")
	}

	logger.Info("Server exiting")
}
