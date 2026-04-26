package tlog

import (
	"io"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/json-iterator/go"
	"github.com/rs/zerolog"
)

var (
	// sentryInitState tracks the lifecycle of the package-level Sentry client.
	sentryInitState atomic.Uint32

	sentryCaptureMessage = sentry.CaptureMessage
	sentryFlush          = sentry.Flush
)

const sentryFlushTimeout = 2 * time.Second

const (
	sentryInitAttempts = 4

	sentryInitIdle uint32 = iota
	sentryInitStarting
	sentryInitReady
	sentryInitFailed
)

// initSentry attempts to initialize the Sentry client with bounded retries.
// On success it returns nil. On failure it returns the last error; the caller must log it
// because package initialization runs before the default logger exists.
func initSentry(sentryDsn string) error {
	var err error

	for attempt := 1; attempt <= sentryInitAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Second)
		}

		err = connectSentry(sentryDsn)
		if err != nil {
			continue
		}

		return nil
	}

	return err
}

func beginSentryInit() {
	sentryInitState.Store(sentryInitStarting)
}

func finishSentryInit(err error) {
	if err == nil {
		sentryInitState.Store(sentryInitReady)
	} else {
		sentryInitState.Store(sentryInitFailed)
	}
}

// connectSentry initializes the Sentry client with the given DSN and stack traces enabled.
func connectSentry(sentryDsn string) error {
	return sentry.Init(sentry.ClientOptions{
		Dsn:              sentryDsn,
		AttachStacktrace: true,
	})
}

// SentryWriter implements zerolog.LevelWriter by forwarding error-level JSON lines
// to Sentry after the Sentry client has been initialized successfully.
type SentryWriter struct {
	io.Writer
}

// simpleLog holds zerolog JSON fields used to build a short title before the raw line.
type simpleLog struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// WriteLevel implements zerolog.LevelWriter. At or above Error it sends the payload to Sentry when
// integration is active. It always returns (len(p), nil) so the zerolog pipeline does not fail.
func (w SentryWriter) WriteLevel(level zerolog.Level, p []byte) (n int, err error) {
	if level >= zerolog.ErrorLevel {
		captureSentryPayload(p)
	}

	return len(p), nil
}

// Close flushes buffered Sentry events when Sentry is ready.
func (w SentryWriter) Close() error {
	flushSentry()

	return nil
}

func captureSentryPayload(p []byte) {
	if sentryInitState.Load() == sentryInitReady {
		sentryCaptureMessage(buildSentryMessage(p))
	}
}

func buildSentryMessage(p []byte) string {
	var log simpleLog

	if err := jsoniter.Unmarshal(p, &log); err == nil {
		if log.Kind != "" {
			log.Message += "[" + log.Kind + "]"
		}

		return log.Message + "\n" + string(p)
	}

	return string(p)
}

func flushSentry() bool {
	if sentryInitState.Load() != sentryInitReady {
		return false
	}

	return sentryFlush(sentryFlushTimeout)
}
