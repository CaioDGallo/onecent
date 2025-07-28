//go:build !dev

package logger

import "os"

func Info(msg string) {
	// No-op in production for maximum performance
}

func Error(msg string) {
	// No-op in production for maximum performance
}

func Fatal(msg string) {
	// Still exit in production for critical failures
	os.Exit(1)
}