// Package tlog provides structured application logging built on top of zerolog.
//
// During package initialization, tlog configures a process-wide default logger from
// values read through github.com/choveylee/tcfg. The default logger can write to
// standard output, an optional rotating log file, and optional Sentry reporting for
// error-level events.
//
// When a context carries a valid trace identifier from github.com/choveylee/ttrace,
// the value is emitted as the trace_id field (see [CtxTraceId]).
//
// Use [D], [I], [W], [E], [F], or [P] to create an event at the corresponding level.
// Add optional fields with [Tevent.Detail], [Tevent.Detailf], and [Tevent.Err], then
// emit the record with [Tevent.Msg] or [Tevent.Msgf].
//
// # Configuration
//
// Exported constants such as [AppName], [LogLevel], [LogFileEnable], and [SentryDsn]
// define the configuration keys consumed through tcfg, typically in combination with
// tcfg.LocalKey. The [LogLevelDebug] through [LogLevelPanic] constants define the
// supported log level names.
package tlog
