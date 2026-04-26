package tlog

import (
	"context"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/choveylee/tcfg"
	"github.com/choveylee/ttrace"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var defaultLog *Tlog

var (
	initSentryFunc = initSentry

	sentryInitLogf = func(format string, args ...any) {
		stdlog.Printf(format, args...)
	}
)

const (
	// CtxTraceId is the field key used for the distributed trace identifier in structured output.
	CtxTraceId string = "trace_id"

	// maxDetailLen is the maximum length of the joined detail payload before truncation.
	maxDetailLen = 10000
)

// Tlog wraps the zerolog.Logger used as the package default after initialization.
type Tlog struct {
	logger zerolog.Logger
}

// Tevent accumulates fields for a single log record. Call [Tevent.Detail], [Tevent.Detailf],
// and optionally [Tevent.Err], then [Tevent.Msg] or [Tevent.Msgf] to emit.
type Tevent struct {
	event *zerolog.Event
	level zerolog.Level

	// details stores fragments from Detail and Detailf, joined into field "detail" on emit.
	details []string
}

func init() {
	zerolog.TimeFieldFormat = time.RFC3339

	logLevel := tcfg.DefaultString(tcfg.LocalKey(LogLevel), "INFO")
	setGlobalLevel(logLevel)

	sentryDsn := tcfg.DefaultString(tcfg.LocalKey(SentryDsn), "")
	if sentryDsn != "" {
		startSentryInit(sentryDsn)
	}

	appName := tcfg.DefaultString(AppName, "")
	if appName == "" {
		_, fileName := filepath.Split(os.Args[0])
		fileExt := filepath.Ext(os.Args[0])

		appName = strings.TrimSuffix(fileName, fileExt)
	}

	var writer zerolog.LevelWriter

	logFileEnable := tcfg.DefaultBool(tcfg.LocalKey(LogFileEnable), false)
	if logFileEnable {
		filePath := tcfg.DefaultString(tcfg.LocalKey(LogFilePath), fmt.Sprintf("%s.log", appName))

		fileSize := tcfg.DefaultInt(tcfg.LocalKey(LogFileSize), 500)

		fileRotate := tcfg.DefaultInt(tcfg.LocalKey(LogFileRotate), 1)

		fileExpired := tcfg.DefaultInt(tcfg.LocalKey(LogFileExpired), 0)
		fileCount := tcfg.DefaultInt(tcfg.LocalKey(LogFileCount), 0)

		fileCompress := tcfg.DefaultBool(tcfg.LocalKey(LogFileCompress), false)

		rotateWriter := newRotateWriter(filePath, fileSize, fileRotate, fileExpired, fileCount, fileCompress)

		writer = zerolog.MultiLevelWriter(os.Stdout, &SentryWriter{}, noCloseWriter{Writer: rotateWriter})
	} else {
		writer = zerolog.MultiLevelWriter(os.Stdout, &SentryWriter{})
	}

	defaultLog = &Tlog{
		logger: log.Logger.With().Str("app_name", appName).Logger().Output(writer),
	}
}

func startSentryInit(sentryDsn string) {
	beginSentryInit()

	go func() {
		err := initSentryFunc(sentryDsn)
		finishSentryInit(err)

		if err != nil {
			sentryInitLogf(
				"tlog: Sentry initialization failed after %d attempts; event reporting is disabled: %v",
				sentryInitAttempts,
				err,
			)
		}
	}()
}

// setGlobalLevel applies the global zerolog level from a case-insensitive name. Unrecognized
// names default to info.
func setGlobalLevel(level string) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "INFO":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "WARN":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "ERROR":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "FATAL":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "PANIC":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

// newTevent constructs a Tevent for the given level string. Levels ERROR, FATAL, and PANIC
// attach a caller field via addCaller.
func newTevent(level string, tl *Tlog) *Tevent {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return &Tevent{
			event: tl.logger.Debug(),
			level: zerolog.DebugLevel,
		}
	case "INFO":
		return &Tevent{
			event: tl.logger.Info(),
			level: zerolog.InfoLevel,
		}
	case "WARN":
		return &Tevent{
			event: tl.logger.Warn(),
			level: zerolog.WarnLevel,
		}
	case "ERROR":
		return addCaller(&Tevent{
			event: tl.logger.Error(),
			level: zerolog.ErrorLevel,
		})
	case "FATAL":
		return addCaller(&Tevent{
			event: tl.logger.Fatal(),
			level: zerolog.FatalLevel,
		})
	case "PANIC":
		return addCaller(&Tevent{
			event: tl.logger.Panic(),
			level: zerolog.PanicLevel,
		})
	default:
		return &Tevent{
			event: tl.logger.Info(),
			level: zerolog.InfoLevel,
		}
	}
}

// D returns a debug-level Tevent for the default logger, enriching the context trace ID when present.
func D(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("DEBUG", defaultLog), ctx)
}

// I returns an info-level Tevent for the default logger, enriching the context trace ID when present.
func I(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("INFO", defaultLog), ctx)
}

// W returns a warn-level Tevent for the default logger, enriching the context trace ID when present.
func W(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("WARN", defaultLog), ctx)
}

// E returns an error-level Tevent with caller metadata and the context trace ID when present.
func E(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("ERROR", defaultLog), ctx)
}

// F returns a fatal-level Tevent with caller metadata and the context trace ID when present.
func F(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("FATAL", defaultLog), ctx)
}

// P returns a panic-level Tevent with caller metadata and the context trace ID when present.
func P(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("PANIC", defaultLog), ctx)
}

// Detail appends value to the detail buffer for the next call to [Tevent.Msg] or [Tevent.Msgf].
func (p *Tevent) Detail(value string) *Tevent {
	if !p.enabled() {
		return p
	}

	p.details = append(p.details, value)
	return p
}

// Detailf appends formatted text to the detail buffer using fmt.Sprintf.
func (p *Tevent) Detailf(format string, a ...any) *Tevent {
	if !p.enabled() {
		return p
	}

	p.details = append(p.details, fmt.Sprintf(format, a...))
	return p
}

// Err records err as field "error" when err is non-nil.
func (p *Tevent) Err(err error) *Tevent {
	if err != nil && p.enabled() {
		p.event = p.event.Str("error", err.Error())
	}

	return p
}

// Msg writes the event with the provided message and returns the same string.
func (p *Tevent) Msg(content string) string {
	if !p.enabled() {
		return content
	}

	p.attachDetail()

	if p.level == zerolog.PanicLevel {
		defer flushSentry()
	}

	p.event.Msg(content)

	return content
}

// Msgf formats and writes the event message, then returns the formatted string.
func (p *Tevent) Msgf(format string, a ...any) string {
	var content string

	if !p.enabled() {
		return fmt.Sprintf(format, a...)
	}

	p.attachDetail()

	if p.level == zerolog.PanicLevel {
		defer flushSentry()
	}

	p.event.MsgFunc(func() string {
		content = fmt.Sprintf(format, a...)
		return content
	})

	return content
}

// injectTraceId adds CtxTraceId to the event when ctx holds a valid trace ID.
func injectTraceId(revent *Tevent, ctx context.Context) *Tevent {
	if !revent.enabled() {
		return revent
	}

	traceId := ttrace.GetTraceId(ctx)
	if ttrace.ValidTraceId(traceId) {
		revent.event = revent.event.Str(CtxTraceId, traceId.String())
	}

	return revent
}

// sizeCheck truncates value to maxDetailLen bytes, appending an ellipsis when truncated.
func sizeCheck(value string) string {
	if len(value) <= maxDetailLen {
		return value
	}
	return value[:maxDetailLen] + "..."
}

// addCaller sets field "caller" to file:line for the first frame outside module path github.com/choveylee.
func addCaller(tevent *Tevent) *Tevent {
	if !tevent.enabled() {
		return tevent
	}

	_, file, line := funcFileLine("github.com/choveylee")

	tevent.event = tevent.event.Str("caller", fmt.Sprintf("%s:%d", file, line))

	return tevent
}

func (p *Tevent) attachDetail() {
	if len(p.details) == 0 {
		return
	}

	value := sizeCheck(strings.Join(p.details, ";"))

	p.event = p.event.Str("detail", value)
}

func (p *Tevent) enabled() bool {
	return p != nil && p.event.Enabled()
}

type noCloseWriter struct {
	io.Writer
}

// funcFileLine inspects the call stack, skips frames whose function name contains excludePKG,
// and returns the short function name, file path, and line for the first remaining frame.
func funcFileLine(excludePKG string) (string, string, int) {
	const depth = 8
	var pcs [depth]uintptr
	n := runtime.Callers(3, pcs[:])
	ff := runtime.CallersFrames(pcs[:n])

	var fn, file string
	var line int
	for {
		f, ok := ff.Next()
		if !ok {
			break
		}

		fn, file, line = f.Function, f.File, f.Line
		if !strings.Contains(fn, excludePKG) {
			break
		}
	}

	if index := strings.LastIndexByte(fn, '/'); index != -1 {
		fn = fn[index+1:]
	}

	return fn, file, line
}
