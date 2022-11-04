package tlog

import (
	"context"
	"io"
	"time"

	"github.com/choveylee/tcfg"
	"github.com/getsentry/sentry-go"
	"github.com/json-iterator/go"
	"github.com/rs/zerolog"
)

var (
	sentryDsn string

	sentryEnable bool
)

func initSentry() error {
	sentryDsn := tcfg.DefaultString(SentryDsn, "")
	if sentryDsn == "" {
		return nil
	}

	retryCount := int32(0)

	for {
		err := connectSentry()
		if err != nil {
			D(context.Background()).Err(err).Msgf("init sentry (%s) err (connect sentry %v).", sentryDsn, err)
		} else {
			sentryEnable = true

			break
		}

		time.Sleep(time.Second)

		retryCount++

		if retryCount > 3 {
			break
		}
	}

	return nil
}

func connectSentry() error {
	options := sentry.ClientOptions{
		Dsn:              sentryDsn,
		AttachStacktrace: true,
	}

	err := sentry.Init(options)
	if err != nil {
		return err
	}

	return nil
}

type SentryWriter struct {
	io.Writer
}

type simpleLog struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

func (w SentryWriter) WriteLevel(level zerolog.Level, p []byte) (n int, err error) {
	if level >= zerolog.ErrorLevel && sentryEnable == true {
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
