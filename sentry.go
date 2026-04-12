package tlog

import (
	"io"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/json-iterator/go"
	"github.com/rs/zerolog"
)

var (
	// sentryEnable indicates whether sentry.Init has completed successfully at least once.
	sentryEnable bool
)

// initSentry calls sentry.Init with bounded retries (four attempts, one second apart).
// On success it enables sentryEnable and returns nil. On failure it returns the last error;
// the caller must log it because package initialization runs before the default logger exists.
func initSentry(sentryDsn string) error {
	var lastErr error
	for attempt := 1; attempt <= 4; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Second)
		}
		err := connectSentry(sentryDsn)
		if err != nil {
			lastErr = err
			continue
		}
		sentryEnable = true
		return nil
	}
	return lastErr
}

// connectSentry initializes the Sentry client with the given DSN and stack traces enabled.
func connectSentry(sentryDsn string) error {
	return sentry.Init(sentry.ClientOptions{
		Dsn:              sentryDsn,
		AttachStacktrace: true,
	})
}

// SentryWriter implements zerolog.LevelWriter by forwarding error-level JSON lines to Sentry when enabled.
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
	if level >= zerolog.ErrorLevel && sentryEnable {
		var log simpleLog

		err := jsoniter.Unmarshal(p, &log)
		if err == nil {
			if log.Kind != "" {
				log.Message += "[" + log.Kind + "]"
			}

			sentry.CaptureMessage(log.Message + "\n" + string(p))
		} else {
			sentry.CaptureMessage(string(p))
		}
	}

	return len(p), nil
}
