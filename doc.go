// Package tlog provides structured logging for Go applications on top of zerolog.
//
// During package initialization, tlog constructs a process-wide default logger
// from configuration values read through github.com/choveylee/tcfg. The default
// logger writes to standard output and can additionally write to a rotating log
// file or forward error-level events to Sentry.
//
// When a context carries a valid trace identifier from github.com/choveylee/ttrace,
// tlog emits the value under the trace_id field (see [CtxTraceId]).
//
// Use [D], [I], [W], [E], [F], or [P] to create an event at the corresponding
// severity. Add optional fields with [Tevent.Detail], [Tevent.Detailf], and
// [Tevent.Err], then emit the record with [Tevent.Msg] or [Tevent.Msgf].
//
// # Configuration
//
// Exported constants such as [AppName], [LogLevel], [LogFileEnable], and
// [SentryDsn] identify the configuration keys consumed through tcfg, typically
// alongside tcfg.LocalKey. The [LogLevelDebug] through [LogLevelPanic] constants
// define the supported severity names.
package tlog
