package logging

import (
	"context"
	"log/slog"
	"os"
)

type Logger struct {
	*slog.Logger
}

func (l *Logger) Fatal(msg string, args ...any) {
	l.Error(msg, args...)
	os.Exit(1)
}

func (l *Logger) FatalContext(ctx context.Context, msg string, args ...any) {
	l.ErrorContext(ctx, msg, args...)
	os.Exit(1)
}

func New(handler slog.Handler) *Logger {
	return &Logger{
		Logger: slog.New(handler),
	}
}
