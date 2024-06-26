package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/lmittmann/tint"
)

func InitLogger(jsonOut, debug bool) *slog.Logger {

	var logLevel slog.Level
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	if debug {
		logLevel = slog.LevelDebug
	}

	var handler slog.Handler
	if jsonOut {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource:   true,
			Level:       logLevel,
			ReplaceAttr: slogFormatter,
		})

	} else {
		handler = tint.NewHandler(os.Stdout,
			&tint.Options{
				AddSource:   debug,
				Level:       logLevel,
				ReplaceAttr: nil,
				NoColor:     false,
				TimeFormat:  time.RFC3339,
			},
		)
	}
	return slog.New(handler)
}

func slogFormatter(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		if _, ok := a.Value.Any().(*time.Time); !ok {
			return a
		}
		a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339))
	}
	if a.Key == slog.SourceKey {
		if _, ok := a.Value.Any().(*slog.Source); !ok {
			return a
		}
		source := a.Value.Any().(*slog.Source)
		source.File = filepath.Base(source.File)
		// Rename attribute name "source" to "source_info"
		// to avoid conflict with "source" attribute in Opensearch index.
		a.Key = "source_info"
	}
	return a
}
