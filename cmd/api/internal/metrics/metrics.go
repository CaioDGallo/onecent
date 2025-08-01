package metrics

import (
	"bytes"
	"os"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

var (
	// Feature flag for enabling/disabling metrics
	metricsEnabled bool

	// HTTP metrics
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)


	// Business metrics
	paymentProcessorRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_processor_requests_total",
			Help: "Total payment processor requests",
		},
		[]string{"processor", "status"},
	)

	circuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=half-open, 2=open)",
		},
		[]string{"processor"},
	)

	// Channel metrics
	channelIngestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_channel_ingest_total",
			Help: "Total number of payment tasks ingested into channels",
		},
		[]string{"channel", "status"},
	)

	channelProcessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_channel_process_total",
			Help: "Total number of payment tasks processed from channels",
		},
		[]string{"channel"},
	)

	channelSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "payment_channel_size",
			Help: "Current size of payment channels",
		},
		[]string{"channel"},
	)

	channelProcessDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_channel_process_duration_seconds",
			Help:    "Time spent processing payment tasks from channels",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
		},
		[]string{"channel"},
	)

	// Worker pool metrics
	workerPoolProcessTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_pool_process_total",
			Help: "Total number of tasks processed by worker pool mode",
		},
		[]string{"channel", "mode"}, // mode: "async" or "sync"
	)

	workerPoolUtilization = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "worker_pool_utilization",
			Help: "Current worker pool utilization (workers in use / total workers)",
		},
		[]string{"channel"},
	)

	// Ants pool metrics
	antsPoolRunning = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ants_pool_running_workers",
			Help: "Number of currently running workers in ants pool",
		},
		[]string{"pool_type"},
	)

	antsPoolCapacity = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ants_pool_capacity",
			Help: "Total capacity of ants pool",
		},
		[]string{"pool_type"},
	)

	antsPoolFree = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ants_pool_free_workers",
			Help: "Number of free workers in ants pool",
		},
		[]string{"pool_type"},
	)

	antsPoolOverloadTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ants_pool_overload_total",
			Help: "Total number of ants pool overload errors",
		},
		[]string{"pool_type"},
	)
)

func Init() {
	// Check if metrics are enabled via environment variable
	if enabled := os.Getenv("ENABLE_METRICS"); enabled != "true" {
		metricsEnabled = false
		return
	}

	metricsEnabled = true

	// Register only our custom metrics (Go collector is already registered by default)
	prometheus.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		paymentProcessorRequests,
		circuitBreakerState,
		channelIngestTotal,
		channelProcessTotal,
		channelSize,
		channelProcessDuration,
		workerPoolProcessTotal,
		workerPoolUtilization,
		antsPoolRunning,
		antsPoolCapacity,
		antsPoolFree,
		antsPoolOverloadTotal,
	)
}

func IsEnabled() bool {
	return metricsEnabled
}

func GetHandler() fiber.Handler {
	if !metricsEnabled {
		return func(c fiber.Ctx) error {
			return c.Status(404).SendString("Metrics disabled")
		}
	}

	return func(c fiber.Ctx) error {
		gatherer := prometheus.DefaultGatherer
		metricFamilies, err := gatherer.Gather()
		if err != nil {
			return c.Status(500).SendString("Error gathering metrics")
		}

		c.Set("Content-Type", string(expfmt.FmtText))
		
		// Use bytes.Buffer for Fiber v3 compatibility
		var buf bytes.Buffer
		encoder := expfmt.NewEncoder(&buf, expfmt.FmtText)
		for _, mf := range metricFamilies {
			if err := encoder.Encode(mf); err != nil {
				return c.Status(500).SendString("Error encoding metrics")
			}
		}
		
		return c.Send(buf.Bytes())
	}
}

func HTTPMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		if !metricsEnabled {
			return c.Next()
		}

		start := time.Now()
		
		err := c.Next()
		
		duration := time.Since(start).Seconds()
		method := c.Method()
		path := c.Route().Path
		status := strconv.Itoa(c.Response().StatusCode())

		httpRequestsTotal.WithLabelValues(method, path, status).Inc()
		httpRequestDuration.WithLabelValues(method, path).Observe(duration)

		return err
	}
}

func RecordPaymentProcessor(processor, status string) {
	if !metricsEnabled {
		return
	}
	paymentProcessorRequests.WithLabelValues(processor, status).Inc()
}

func SetCircuitBreakerState(processor string, state int) {
	if !metricsEnabled {
		return
	}
	circuitBreakerState.WithLabelValues(processor).Set(float64(state))
}

func RecordChannelIngest(channel, status string) {
	if !metricsEnabled {
		return
	}
	channelIngestTotal.WithLabelValues(channel, status).Inc()
}

func RecordChannelProcess(channel string) {
	if !metricsEnabled {
		return
	}
	channelProcessTotal.WithLabelValues(channel).Inc()
}

func UpdateChannelSize(channel string, size int) {
	if !metricsEnabled {
		return
	}
	channelSize.WithLabelValues(channel).Set(float64(size))
}

func RecordChannelProcessDuration(channel string, duration float64) {
	if !metricsEnabled {
		return
	}
	channelProcessDuration.WithLabelValues(channel).Observe(duration)
}

func RecordWorkerPoolProcess(channel, mode string) {
	if !metricsEnabled {
		return
	}
	workerPoolProcessTotal.WithLabelValues(channel, mode).Inc()
}

func UpdateWorkerPoolUtilization(channel string, workersInUse, totalWorkers int) {
	if !metricsEnabled {
		return
	}
	utilization := float64(workersInUse) / float64(totalWorkers)
	workerPoolUtilization.WithLabelValues(channel).Set(utilization)
}

func UpdateAntsPoolMetrics(poolType string, running, capacity, free int) {
	if !metricsEnabled {
		return
	}
	antsPoolRunning.WithLabelValues(poolType).Set(float64(running))
	antsPoolCapacity.WithLabelValues(poolType).Set(float64(capacity))
	antsPoolFree.WithLabelValues(poolType).Set(float64(free))
}

func RecordAntsPoolOverload(poolType string) {
	if !metricsEnabled {
		return
	}
	antsPoolOverloadTotal.WithLabelValues(poolType).Inc()
}

