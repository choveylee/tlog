package tlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choveylee/tcfg"
	"github.com/choveylee/ttrace"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var defaultLog *Tlog

const (
	// CtxTraceId is the field key used for distributed trace identifiers.
	CtxTraceId string = "trace_id"

	// maxDetailLen limits retained detail bytes before the truncation suffix.
	maxDetailLen = 10000
)

// Tlog owns the default structured logger and its optional rotating file.
type Tlog struct {
	logger       zerolog.Logger
	rotateWriter *RotateWriter
}

// Tevent builds one structured log record. Finish it with Msg or Msgf once;
// events must not be copied, reused after emission, or shared across goroutines.
type Tevent struct {
	event *zerolog.Event
	level zerolog.Level

	details         strings.Builder
	hasDetail       bool
	detailTruncated bool
}

// init configures owned outputs before publishing the process-wide logger.
// File/Sentry configuration failures preserve stdout logging and are reported without DSNs.
func init() {
	zerolog.TimeFieldFormat = time.RFC3339

	logLevel := strings.TrimSpace(tcfg.DefaultString(tcfg.LocalKey(LogLevel), "INFO"))
	setGlobalLevel(logLevel)

	sentryDsn := strings.TrimSpace(tcfg.DefaultString(tcfg.LocalKey(SentryDsn), ""))
	if sentryDsn != "" {
		if err := initSentry(sentryDsn); err != nil {
			// SDK parse errors may contain the full DSN, including credentials.
			stdlog.Print("tlog: initialize Sentry: invalid client configuration; error forwarding disabled")
		}
	}

	appName := strings.TrimSpace(tcfg.DefaultString(AppName, ""))
	if appName == "" {
		fileName := filepath.Base(os.Args[0])
		appName = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}

	var rotateWriter *RotateWriter
	if tcfg.DefaultBool(tcfg.LocalKey(LogFileEnable), false) {
		filePath := strings.TrimSpace(tcfg.DefaultString(tcfg.LocalKey(LogFilePath), fmt.Sprintf("%s.log", appName)))
		fileSize := tcfg.DefaultInt(tcfg.LocalKey(LogFileSize), 500)
		fileRotate := tcfg.DefaultInt(tcfg.LocalKey(LogFileRotate), 1)
		fileExpired := tcfg.DefaultInt(tcfg.LocalKey(LogFileExpired), 0)
		fileCount := tcfg.DefaultInt(tcfg.LocalKey(LogFileCount), 0)
		fileCompress := tcfg.DefaultBool(tcfg.LocalKey(LogFileCompress), false)

		var err error
		rotateWriter, err = newRotateWriter(filePath, fileSize, fileRotate, fileExpired, fileCount, fileCompress)
		if err != nil {
			stdlog.Printf("tlog: initialize rotating log file: %v; continuing with stdout", err)
		}
	}

	sentryWriter := SentryWriter{}
	outputs := []io.Writer{os.Stdout, sentryWriter}
	if rotateWriter != nil {
		outputs = append(outputs, rotateWriter)
	}

	multiWriter := zerolog.MultiLevelWriter(outputs...)
	output := shutdownWriter{LevelWriter: multiWriter}
	logger := log.Logger.With().Str("app_name", appName).Logger().Output(output)
	defaultLogger := &Tlog{logger: logger, rotateWriter: rotateWriter}

	defaultLog = defaultLogger
}

// Shutdown closes the owned file, waits for final backup maintenance, and flushes
// Sentry with a two-second timeout. Stop producers before calling it. Repeated calls are safe;
// stdout remains open. This function does not shut down tracing owned by ttrace.
func Shutdown() error {
	var err error
	if defaultLog != nil && defaultLog.rotateWriter != nil {
		err = defaultLog.rotateWriter.Close()
	}

	sentryWriter := SentryWriter{}

	return errors.Join(err, sentryWriter.Close())
}

// D returns a debug event with the context's valid trace ID when available.
func D(ctx context.Context) *Tevent {
	return injectTraceId(newTevent(zerolog.DebugLevel, defaultLog), ctx)
}

// I returns an info event with the context's valid trace ID when available.
func I(ctx context.Context) *Tevent {
	return injectTraceId(newTevent(zerolog.InfoLevel, defaultLog), ctx)
}

// W returns a warning event with the context's valid trace ID when available.
func W(ctx context.Context) *Tevent {
	return injectTraceId(newTevent(zerolog.WarnLevel, defaultLog), ctx)
}

// E returns an error event with caller metadata and the context's valid trace ID.
func E(ctx context.Context) *Tevent {
	return injectTraceId(newTevent(zerolog.ErrorLevel, defaultLog), ctx)
}

// F returns a fatal event. Emitting it closes owned outputs before zerolog's fatal exit.
func F(ctx context.Context) *Tevent {
	return injectTraceId(newTevent(zerolog.FatalLevel, defaultLog), ctx)
}

// P returns a panic event. Emitting it flushes Sentry before the panic propagates.
func P(ctx context.Context) *Tevent {
	return injectTraceId(newTevent(zerolog.PanicLevel, defaultLog), ctx)
}

// Detail appends a fragment within the shared 10000-byte budget, separated by semicolons.
// Truncation respects UTF-8 boundaries and does not retain the original oversized string.
func (logEvent *Tevent) Detail(value string) *Tevent {
	if !logEvent.enabled() || logEvent.detailTruncated {
		return logEvent
	}

	if logEvent.hasDetail {
		if logEvent.details.Len() == maxDetailLen {
			logEvent.detailTruncated = true

			return logEvent
		}

		logEvent.details.WriteByte(';')
	}

	logEvent.hasDetail = true
	remaining := maxDetailLen - logEvent.details.Len()
	if len(value) > remaining {
		for remaining > 0 && !utf8.RuneStart(value[remaining]) {
			remaining--
		}

		value = value[:remaining]
		logEvent.detailTruncated = true
	}

	logEvent.details.WriteString(value)

	return logEvent
}

// Detailf formats a fragment when enabled and the detail budget has not been exhausted.
// Formatting happens before truncation; arbitrary formatters may allocate their own buffers.
func (logEvent *Tevent) Detailf(format string, arguments ...any) *Tevent {
	if !logEvent.enabled() || logEvent.detailTruncated {
		return logEvent
	}

	if logEvent.hasDetail && logEvent.details.Len() == maxDetailLen {
		logEvent.detailTruncated = true

		return logEvent
	}

	return logEvent.Detail(fmt.Sprintf(format, arguments...))
}

// Err records a non-nil error under the error field.
func (logEvent *Tevent) Err(err error) *Tevent {
	if err != nil && logEvent.enabled() {
		logEvent.event = logEvent.event.Str("error", err.Error())
	}

	return logEvent
}

// Msg emits this event once and returns content even when logging is filtered.
func (logEvent *Tevent) Msg(content string) string {
	if !logEvent.enabled() {
		return content
	}

	if logEvent.hasDetail {
		detail := logEvent.details.String()
		if logEvent.detailTruncated {
			detail += "..."
		}

		logEvent.event.Str("detail", detail)
	}

	output := logEvent.event
	logEvent.event = nil
	logEvent.details.Reset()

	if logEvent.level == zerolog.PanicLevel {
		defer flushSentry()
	}

	output.Msg(content)

	return content
}

// Msgf formats once and returns the message even when output is filtered.
// Emission and consumption follow Msg's single-use contract.
func (logEvent *Tevent) Msgf(format string, arguments ...any) string {
	return logEvent.Msg(fmt.Sprintf(format, arguments...))
}

// setGlobalLevel applies supported configuration names, preserving the legacy info fallback.
func setGlobalLevel(level string) {
	switch strings.ToUpper(level) {
	case LogLevelDebug:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case LogLevelInfo:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case LogLevelWarn:
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case LogLevelError:
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case LogLevelFatal:
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case LogLevelPanic:
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	default:
		stdlog.Print("tlog: log level must be DEBUG, INFO, WARN, ERROR, FATAL, or PANIC; using INFO")
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

// newTevent creates the corresponding zerolog event and locates the first caller
// outside this package for error/fatal/panic records, including callers in the same organization.
func newTevent(level zerolog.Level, logger *Tlog) *Tevent {
	logEvent := &Tevent{level: level}
	switch level {
	case zerolog.DebugLevel:
		logEvent.event = logger.logger.Debug()
	case zerolog.WarnLevel:
		logEvent.event = logger.logger.Warn()
	case zerolog.ErrorLevel:
		logEvent.event = logger.logger.Error()
	case zerolog.FatalLevel:
		logEvent.event = logger.logger.Fatal()
	case zerolog.PanicLevel:
		logEvent.event = logger.logger.Panic()
	default:
		logEvent.event = logger.logger.Info()
	}

	if level < zerolog.ErrorLevel || !logEvent.enabled() {
		return logEvent
	}

	var programCounters [16]uintptr
	frameCount := runtime.Callers(2, programCounters[:])
	frames := runtime.CallersFrames(programCounters[:frameCount])
	for {
		frame, more := frames.Next()
		if frame.Function != "" && !strings.HasPrefix(frame.Function, "github.com/choveylee/tlog.") {
			logEvent.event.Str("caller", fmt.Sprintf("%s:%d", frame.File, frame.Line))

			break
		}

		if !more {
			break
		}
	}

	return logEvent
}

// injectTraceId adds trace_id when the context carries a valid trace identifier.
func injectTraceId(logEvent *Tevent, ctx context.Context) *Tevent {
	if !logEvent.enabled() {
		return logEvent
	}

	traceId := ttrace.GetTraceId(ctx)
	if ttrace.ValidTraceId(traceId) {
		logEvent.event.Str(CtxTraceId, traceId.String())
	}

	return logEvent
}

func (logEvent *Tevent) enabled() bool {
	return logEvent != nil && logEvent.event.Enabled()
}

// shutdownWriter adapts zerolog's fatal cleanup to the resources owned by tlog;
// closing the underlying MultiLevelWriter directly would also close process-owned stdout.
type shutdownWriter struct {
	zerolog.LevelWriter
}

func (writer shutdownWriter) Close() error {
	return Shutdown()
}
