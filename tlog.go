/**
 * @Author: lidonglin
 * @Description: Package entry: zerolog-based logger, tcfg, optional rotation, Sentry, ttrace
 * @File:  tlog.go
 * @Version: 1.0.0
 * @Date: 2022/10/12 17:47
 */

// Package tlog provides structured logging built on zerolog. At process startup it loads
// settings from tcfg: minimum log level, optional file output with rotation, and an optional
// Sentry DSN. When a context carries a valid trace ID from package ttrace, it is emitted as
// field trace_id.
//
// The package exposes one configured [Tlog] as the default logger. Use [D], [I], [W], [E], [F],
// or [P] to start an event, then chain [Tevent.Detail], [Tevent.Detailf], and optionally
// [Tevent.Err], and finish with [Tevent.Msg] or [Tevent.Msgf].
package tlog

import (
	"context"
	"fmt"
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

const (
	// CtxTraceId is the JSON field name for the trace identifier when present in ctx.
	CtxTraceId string = "trace_id"

	// maxDetailLen caps the length of the joined detail string before it is written as field "detail".
	maxDetailLen = 10000
)

// Tlog holds the configured zerolog.Logger for the default logger created during package init.
type Tlog struct {
	logger zerolog.Logger
}

// Tevent is a builder for one log line. Chain Detail or Detailf, optionally Err, then Msg or Msgf.
type Tevent struct {
	event *zerolog.Event

	// details holds fragments appended by Detail and Detailf, joined into field "detail" on Msg or Msgf.
	details []string
}

func init() {
	zerolog.TimeFieldFormat = time.RFC3339

	logLevel := tcfg.DefaultString(tcfg.LocalKey(LogLevel), "INFO")
	setGlobalLevel(logLevel)

	sentryDsn := tcfg.DefaultString(tcfg.LocalKey(SentryDsn), "")
	if sentryDsn != "" {
		if err := initSentry(sentryDsn); err != nil {
			fmt.Fprintf(os.Stderr, "tlog: Sentry disabled after 4 failed init attempts (dsn=%q): %v\n", sentryDsn, err)
		}
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

		writer = zerolog.MultiLevelWriter(os.Stdout, &SentryWriter{}, rotateWriter)
	} else {
		writer = zerolog.MultiLevelWriter(os.Stdout, &SentryWriter{})
	}

	defaultLog = &Tlog{
		logger: log.Logger.With().Str("app_name", appName).Logger().Output(writer),
	}
}

// setGlobalLevel maps a case-insensitive level name to zerolog.SetGlobalLevel; unknown names default to info.
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

// newTevent returns a Tevent for the named level. ERROR, FATAL, and PANIC events include a caller field.
func newTevent(level string, tlog *Tlog) *Tevent {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return &Tevent{
			event: tlog.logger.Debug(),
		}
	case "INFO":
		return &Tevent{
			event: tlog.logger.Info(),
		}
	case "WARN":
		return &Tevent{
			event: tlog.logger.Warn(),
		}
	case "ERROR":
		return addCaller(&Tevent{
			event: tlog.logger.Error(),
		})
	case "FATAL":
		return addCaller(&Tevent{
			event: tlog.logger.Fatal(),
		})
	case "PANIC":
		return addCaller(&Tevent{
			event: tlog.logger.Panic(),
		})
	default:
		return &Tevent{
			event: tlog.logger.Info(),
		}
	}
}

// D starts a debug-level event on the default logger and adds trace_id from ctx when available.
func D(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("DEBUG", defaultLog), ctx)
}

// I starts an info-level event on the default logger and adds trace_id from ctx when available.
func I(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("INFO", defaultLog), ctx)
}

// W starts a warn-level event on the default logger and adds trace_id from ctx when available.
func W(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("WARN", defaultLog), ctx)
}

// E starts an error-level event on the default logger, adds caller metadata, and trace_id from ctx when available.
func E(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("ERROR", defaultLog), ctx)
}

// F starts a fatal-level event on the default logger, adds caller metadata, and trace_id from ctx when available.
func F(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("FATAL", defaultLog), ctx)
}

// P starts a panic-level event on the default logger, adds caller metadata, and trace_id from ctx when available.
func P(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("PANIC", defaultLog), ctx)
}

// Detail appends value to the detail buffer. Msg and Msgf join fragments into field "detail".
func (p *Tevent) Detail(value string) *Tevent {
	p.details = append(p.details, value)
	return p
}

// Detailf appends fmt.Sprintf(format, a...) to the detail buffer.
func (p *Tevent) Detailf(format string, a ...any) *Tevent {
	p.details = append(p.details, fmt.Sprintf(format, a...))
	return p
}

// Err sets field "error" to err.Error() when err is not nil.
func (p *Tevent) Err(err error) *Tevent {
	if err != nil {
		p.event = p.event.Str("error", err.Error())
	}

	return p
}

// Msg emits the log line with the current fields and returns content unchanged.
func (p *Tevent) Msg(content string) string {
	if len(p.details) > 0 {
		value := sizeCheck(strings.Join(p.details, ";"))
		p.event = p.event.Str("detail", value)
	}
	p.event.Msg(content)

	return content
}

// Msgf emits fmt.Sprintf(format, a...) as the message and returns that string.
func (p *Tevent) Msgf(format string, a ...any) string {
	if len(p.details) > 0 {
		value := sizeCheck(strings.Join(p.details, ";"))
		p.event = p.event.Str("detail", value)
	}

	content := fmt.Sprintf(format, a...)
	p.event.Msg(content)

	return content
}

// injectTraceId sets field CtxTraceId when ctx contains a valid trace ID from ttrace.
func injectTraceId(revent *Tevent, ctx context.Context) *Tevent {
	traceId := ttrace.GetTraceId(ctx)
	if ttrace.ValidTraceId(traceId) {
		revent.event = revent.event.Str(CtxTraceId, traceId.String())
	}

	return revent
}

// sizeCheck returns value truncated to maxDetailLen bytes with an ellipsis suffix if longer.
func sizeCheck(value string) string {
	if len(value) <= maxDetailLen {
		return value
	}
	return value[:maxDetailLen] + "..."
}

// addCaller sets field "caller" to file:line for the first stack frame outside github.com/choveylee packages.
func addCaller(revent *Tevent) *Tevent {
	_, file, line := funcFileLine("github.com/choveylee")
	revent.event = revent.event.Str("caller", fmt.Sprintf("%s:%d", file, line))
	return revent
}

// funcFileLine walks the stack, skips frames whose full function name contains excludePKG, and returns
// the short function name, file path, and line of the first frame that does not match.
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
