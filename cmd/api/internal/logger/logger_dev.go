//go:build dev

package logger

import (
	"fmt"
	"os"
)

func Info(msg string) {
	fmt.Printf("[INFO] %s\n", msg)
}

func Error(msg string) {
	fmt.Printf("[ERROR] %s\n", msg)
}

func Fatal(msg string) {
	fmt.Printf("[FATAL] %s\n", msg)
	os.Exit(1)
}