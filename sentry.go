package tlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog"
)

var sentryReady atomic.Bool

const sentryFlushTimeout = 2 * time.Second

// initSentry validates and installs the SDK client before logging becomes available.
// SDK construction is local; retrying the same invalid DSN does not recover it.
func initSentry(sentryDsn string) error {
	options := sentry.ClientOptions{Dsn: sentryDsn, AttachStacktrace: true}

	if err := sentry.Init(options); err != nil {
		return fmt.Errorf("tlog: initialize Sentry: %w", err)
	}

	sentryReady.Store(true)

	return nil
}

// SentryWriter implements zerolog.LevelWriter for error/fatal/panic events.
// Its optional embedded Writer handles plain, unlevelled Write calls; a nil Writer discards them.
type SentryWriter struct {
	io.Writer
}

// simpleLog contains the title fields used alongside the complete structured payload.
type simpleLog struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// Write forwards plain data to an explicitly supplied Writer, or consumes it safely when unset.
func (writer SentryWriter) Write(payload []byte) (int, error) {
	if writer.Writer != nil {
		return writer.Writer.Write(payload)
	}

	return len(payload), nil
}

// WriteLevel forwards supported severity levels using an isolated Sentry scope.
// It preserves the logging pipeline's successful-write contract even when forwarding is disabled.
func (writer SentryWriter) WriteLevel(level zerolog.Level, payload []byte) (int, error) {
	if level < zerolog.ErrorLevel || level > zerolog.PanicLevel || !sentryReady.Load() {
		return len(payload), nil
	}

	message := string(payload)
	var logData simpleLog
	if err := json.Unmarshal(payload, &logData); err == nil {
		title := logData.Message
		if logData.Kind != "" {
			if title != "" {
				title += " "
			}

			title += "[" + logData.Kind + "]"
		}

		message = title + "\n" + message
	}

	hub := sentry.CurrentHub().Clone()
	sentryLevel := sentry.LevelError
	if level >= zerolog.FatalLevel {
		sentryLevel = sentry.LevelFatal
	}

	hub.Scope().SetLevel(sentryLevel)
	hub.CaptureMessage(message)

	return len(payload), nil
}

// Close flushes ready Sentry events and reports a timeout without closing an embedded Writer.
func (writer SentryWriter) Close() error {
	if !flushSentry() {
		return errors.New("tlog: flush Sentry timed out")
	}

	return nil
}

// flushSentry waits for queued events; disabled integrations have nothing to flush.
func flushSentry() bool {
	return !sentryReady.Load() || sentry.Flush(sentryFlushTimeout)
}
