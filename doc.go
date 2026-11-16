// Package tlog provides structured logging facilities for Go applications built
// on top of zerolog.
//
// During package initialization, tlog configures a process-wide default logger
// using values read through github.com/choveylee/tcfg. The default logger writes
// to standard output and can optionally write to a rotating log file or forward
// error-level events to Sentry.
//
// When a context carries a valid trace identifier from github.com/choveylee/ttrace,
// tlog emits that value under the trace_id field (see [CtxTraceId]).
//
// Use [D], [I], [W], [E], [F], or [P] to create an event at the corresponding
// severity. Add optional fields with [Tevent.Detail], [Tevent.Detailf], and
// [Tevent.Err], then emit the record with [Tevent.Msg] or [Tevent.Msgf].
// Events are single-use; retained detail fragments share a UTF-8-aware 10000-byte budget.
// Stop producers and call [Shutdown] before process exit to close owned files, finish backup
// maintenance, and flush Sentry without closing stdout. File paths require exclusive ownership
// by one writer/process. Invalid file or Sentry configuration falls back to the remaining outputs.
//
// # Configuration
//
// Exported constants such as [AppName], [LogLevel], [LogFileEnable], and
// [SentryDsn] identify the configuration keys consumed through tcfg, typically
// in conjunction with tcfg.LocalKey. The [LogLevelDebug] through [LogLevelPanic]
// constants define the supported severity names.
package tlog
