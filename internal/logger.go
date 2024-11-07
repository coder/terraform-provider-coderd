package internal

import (
	"context"

	"cdr.dev/slog"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ slog.Sink = &tflogSink{}

type tflogSink struct {
	ctx context.Context
}

func NewLogSink(ctx context.Context) slog.Sink {
	return &tflogSink{
		ctx: ctx,
	}
}

func (s *tflogSink) LogEntry(ctx context.Context, e slog.SinkEntry) {
	var logFn func(ctx context.Context, msg string, additionalFields ...map[string]interface{})
	switch e.Level {
	case slog.LevelDebug:
		logFn = tflog.Debug
	case slog.LevelInfo:
		logFn = tflog.Info
	case slog.LevelWarn:
		logFn = tflog.Warn
	default:
		logFn = tflog.Error
	}
	logFn(s.ctx, e.Message, mapToFields(e.Fields))
}

func (s *tflogSink) Sync() {}

func mapToFields(m slog.Map) map[string]interface{} {
	fields := make(map[string]interface{}, len(m))
	for _, v := range m {
		fields[v.Name] = v.Value
	}
	return fields
}
