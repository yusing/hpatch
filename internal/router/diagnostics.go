package router

import (
	"context"
	"io"
	"log/slog"
	"time"
)

type diagnostics struct {
	handler slog.Handler
}

func newDiagnostics(writer io.Writer) diagnostics {
	return diagnostics{handler: slog.NewTextHandler(writer, nil)}
}

func (d diagnostics) with(args ...any) diagnostics {
	return diagnostics{handler: slog.New(d.handler).With(args...).Handler()}
}

func (d diagnostics) log(ctx context.Context, level slog.Level, message string, args ...any) error {
	if !d.handler.Enabled(ctx, level) {
		return nil
	}
	record := slog.NewRecord(time.Now(), level, message, 0)
	record.Add(args...)
	return d.handler.Handle(ctx, record)
}
