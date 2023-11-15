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
	CtxTraceId string = "trace_id"
)

type Tlog struct {
	logger zerolog.Logger
}

type Tevent struct {
	event *zerolog.Event

	// store user defined values
	details []string
}

func init() {
	// init zero log
	zerolog.TimeFieldFormat = time.RFC3339

	logLevel := tcfg.DefaultString(tcfg.LocalKey(LogLevel), "INFO")
	setGlobalLevel(logLevel)

	// init sentry
	sentryDsn := tcfg.DefaultString(tcfg.LocalKey(SentryDsn), "")
	if sentryDsn != "" {
		err := initSentry(sentryDsn)
		if err != nil {
			panic(err)
		}
	}

	// init log file & tlog
	appName := tcfg.DefaultString(AppName, "")
	if appName == "" {
		_, fileName := filepath.Split(os.Args[0])
		fileExt := filepath.Ext(os.Args[0])

		appName = strings.TrimSuffix(fileName, fileExt)
	}

	var writer zerolog.LevelWriter

	logFileEnable := tcfg.DefaultBool(tcfg.LocalKey(LogFileEnable), false)
	if logFileEnable == true {
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

// setGlobalLevel set global level
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

// D use debug level log
func D(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("DEBUG", defaultLog), ctx)
}

// I use info level log
func I(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("INFO", defaultLog), ctx)
}

// W use warn level log
func W(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("WARN", defaultLog), ctx)
}

// E use error level log
func E(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("ERROR", defaultLog), ctx)
}

// F use fatal level log
func F(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("FATAL", defaultLog), ctx)
}

// P use panic level log
func P(ctx context.Context) *Tevent {
	return injectTraceId(newTevent("PANIC", defaultLog), ctx)
}

// Detail attach extension log
func (p *Tevent) Detail(value string) *Tevent {
	p.details = append(p.details, value)
	return p
}

// Detailf attach format extension log
func (p *Tevent) Detailf(format string, a ...interface{}) *Tevent {
	p.details = append(p.details, fmt.Sprintf(format, a...))
	return p
}

// Err attach error msg
func (p *Tevent) Err(err error) *Tevent {
	if err != nil {
		p.event = p.event.Str("error", err.Error())
	}

	return p
}

// Msg output msg
func (p *Tevent) Msg(content string) string {
	if len(p.details) > 0 {
		value := sizeCheck(strings.Join(p.details, ";"))
		p.event = p.event.Str("detail", value)
	}
	p.event.Msg(content)

	return content
}

// Msgf output format msg
func (p *Tevent) Msgf(format string, a ...interface{}) string {
	if len(p.details) > 0 {
		value := sizeCheck(strings.Join(p.details, ";"))
		p.event = p.event.Str("detail", value)
	}

	content := fmt.Sprintf(format, a...)
	p.event.Msg(content)

	return content
}

// injectTraceId inject trace id into revent
func injectTraceId(revent *Tevent, ctx context.Context) *Tevent {
	traceId := ttrace.GetTraceID(ctx)
	if ttrace.ValidTraceID(traceId) {
		revent.event = revent.event.Str(CtxTraceId, traceId.String())
	}

	return revent
}

func sizeCheck(value string) string {
	if len(value) > 10000 {
		return value[:1000] + "..."
	} else {
		return value
	}
}

// 获取调用行
func addCaller(revent *Tevent) *Tevent {
	_, file, line := funcFileLine("github.com/choveylee")
	revent.event = revent.event.Str("caller", fmt.Sprintf("%s:%d", file, line))
	return revent
}

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
