package provider

import (
	"context"

	"cdr.dev/slog/v3"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ slog.Sink = &tfLogSink{}

type tfLogSink struct {
	tfCtx context.Context
}

func newTFLogSink(tfCtx context.Context) *tfLogSink {
	return &tfLogSink{
		tfCtx: tfCtx,
	}
}

func (s *tfLogSink) LogEntry(ctx context.Context, e slog.SinkEntry) {
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
	logFn(s.tfCtx, e.Message, mapToFields(e.Fields))
}

func (s *tfLogSink) Sync() {}

func mapToFields(m slog.Map) map[string]interface{} {
	fields := make(map[string]interface{}, len(m))
	for _, v := range m {
		fields[v.Name] = v.Value
	}
	return fields
}
