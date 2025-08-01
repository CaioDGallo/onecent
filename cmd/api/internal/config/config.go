package config

import (
	"os"
	"strconv"
)

const (
	ServerAddress = ":8080"
)

func GetDefaultProcessorEndpoint() string {
	if endpoint := os.Getenv("DEFAULT_PROCESSOR_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return "http://payment-processor-default:8080"
}

func GetFallbackProcessorEndpoint() string {
	if endpoint := os.Getenv("FALLBACK_PROCESSOR_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return "http://payment-processor-fallback:8080"
}

func GetDatabaseConnectionString() string {
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		return dbURL
	}
	return "postgresql://dev:secret123@localhost:5432/onecent?sslmode=disable"
}

func GetInstanceID() string {
	instanceID := os.Getenv("INSTANCE_ID")
	if instanceID == "" {
		instanceID = "1"
	}
	return instanceID
}

func GetOtherInstanceURL() string {
	if url := os.Getenv("OTHER_INSTANCE_URL"); url != "" {
		return url
	}
	return ""
}

func GetPaymentPoolSize() int {
	if size := os.Getenv("PAYMENT_POOL_SIZE"); size != "" {
		if poolSize, err := strconv.Atoi(size); err == nil && poolSize > 0 {
			return poolSize
		}
	}
	return 100 // Default pool size
}

func GetRetryPoolSize() int {
	if size := os.Getenv("RETRY_POOL_SIZE"); size != "" {
		if poolSize, err := strconv.Atoi(size); err == nil && poolSize > 0 {
			return poolSize
		}
	}
	return 50 // Default pool size
}

