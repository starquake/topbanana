package logging_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/starquake/topbanana/internal/logging"
)

func TestNewLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := logging.NewLogger(&buf)

	if logger == nil {
		t.Error("logger is nil")
	}

	if got, want := reflect.TypeFor[*logging.Logger](), reflect.TypeFor[*logging.Logger](); got != want {
		t.Errorf("logger is not of type *logging.Logger: %v", want)
	}
}

func TestNewLoggerWithLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := logging.NewLoggerWithLevel(&buf, logging.LevelDebug)
	if logger == nil {
		t.Error("logger is nil")
	}

	if got, want := reflect.TypeFor[*logging.Logger](), reflect.TypeFor[*logging.Logger](); got != want {
		t.Errorf("logger is not of type *logging.Logger: %v", want)
	}
}

func TestLogMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		logFunc func(*logging.Logger, context.Context, string, ...logging.Attr)
		message string
		wantErr error
	}{
		{
			name:    "Debug",
			logFunc: (*logging.Logger).Debug,
			message: "debug message",
			wantErr: errors.New("debug error"),
		},
		{
			name:    "Info",
			logFunc: (*logging.Logger).Info,
			message: "info message",
			wantErr: errors.New("info error"),
		},
		{
			name:    "Warn",
			logFunc: (*logging.Logger).Warn,
			message: "warn message",
			wantErr: errors.New("warn error"),
		},
		{
			name:    "Error",
			logFunc: (*logging.Logger).Error,
			message: "error message",
			wantErr: errors.New("error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := logging.NewLoggerWithLevel(&buf, logging.LevelDebug)
			loggerAttrs := logging.ErrAttr(tt.wantErr)
			ctx := context.Background()

			tt.logFunc(logger, ctx, tt.message, loggerAttrs)

			got := buf.String()

			if want := tt.message; !strings.Contains(got, want) {
				t.Errorf("message: got %q, want substring %q", got, want)
			}

			if want := tt.wantErr.Error(); !strings.Contains(got, want) {
				t.Errorf("error attr: got %q, want substring %q", got, want)
			}
		})
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	attr := logging.String("name", "renata")
	if got, want := attr.Key, "name"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := attr.Value.String(), "renata"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestErrAttr(t *testing.T) {
	t.Parallel()

	err := errors.New("jedi error")
	attr := logging.ErrAttr(err)
	if got, want := attr.Key, "err"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := attr.Value.String(), err.Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
