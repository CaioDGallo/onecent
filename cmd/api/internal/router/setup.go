package router

import (
	"github.com/gofiber/fiber/v3"

	"github.com/CaioDGallo/onecent/cmd/api/internal/handlers"
)

func SetupRoutes(paymentHandler *handlers.PaymentHandler, statsHandler *handlers.StatsHandler, healthHandler *handlers.HealthHandler) *fiber.App {
	app := fiber.New(fiber.Config{
		BodyLimit:       1024,
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	})

	app.Post("/payments", paymentHandler.CreatePayment)
	app.Get("/payments-summary", statsHandler.GetPaymentsSummary)
	app.Post("/internal/health-sync", healthHandler.SyncHealth)

	return app
}
