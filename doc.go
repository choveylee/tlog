// Package tlog provides structured logging for Go programs using zerolog. Configuration is
// read once at process startup through github.com/choveylee/tcfg: minimum log level, optional
// rotating file output, optional Sentry reporting, and application metadata for fields and paths.
//
// When a context carries a valid trace identifier from github.com/choveylee/ttrace, it is
// emitted as field trace_id (see [CtxTraceId]).
//
// The package exposes a single default [Tlog]. Use [D], [I], [W], [E], [F], or [P] to begin an
// event at the corresponding level; chain [Tevent.Detail], [Tevent.Detailf], and optionally
// [Tevent.Err], then complete the line with [Tevent.Msg] or [Tevent.Msgf].
//
// # Configuration
//
// String constants such as [LogLevel], [SentryDsn], and [LogFileEnable] are keys for tcfg
// (typically combined with tcfg.LocalKey). The [LogLevelDebug] through [LogLevelPanic] constants
// are the accepted level names. See the const declarations in this package for the full set.
package tlog
