/**
 * @Author: lidonglin
 * @Description: Sentry client initialization and error-level log forwarding
 * @File:  sentry.go
 * @Version: 1.0.0
 * @Date: 2022/10/12 17:47
 */

package tlog

import (
	"io"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/json-iterator/go"
	"github.com/rs/zerolog"
)

var (
	// sentryEnable reports whether sentry.Init completed successfully at least once.
	sentryEnable bool
)

// initSentry attempts sentry.Init up to four times with one second between failures.
// It sets sentryEnable on success and returns nil. On exhaustion it returns the last error;
// the caller must report failures (package init runs before the default logger exists).
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

// connectSentry runs sentry.Init with stack traces attached to captured events.
func connectSentry(sentryDsn string) error {
	return sentry.Init(sentry.ClientOptions{
		Dsn:              sentryDsn,
		AttachStacktrace: true,
	})
}

// SentryWriter implements zerolog.LevelWriter. When Sentry is enabled, error-level and fatal JSON
// lines are unmarshalled when possible and sent to Sentry.
type SentryWriter struct {
	io.Writer
}

// simpleLog captures common zerolog JSON fields for building a short title before the raw payload.
type simpleLog struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// WriteLevel implements zerolog.LevelWriter. For levels at or above Error it forwards to Sentry
// when enabled. It always reports len(p) bytes written and a nil error to the logger.
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
