package config

import "os"

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

