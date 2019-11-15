package logger_test

import (
	"go.cantor.systems/logger"
	"testing"
)

func TestNew(t *testing.T) {
	log, err := logger.New(logger.LoggingConfiguration{
		GraylogAddress: "localhost:5141",
		AppName:        "test",
		Hostname:       "localhost",
	})

	if err != nil {
		t.Fatal("error occurred:", err)
	}

	if log == nil {
		t.Fatal("nil apilog")
	}
}
