# tlog

Structured logging for Go, built on [zerolog](https://github.com/rs/zerolog). It integrates [tcfg](https://github.com/choveylee/tcfg) for configuration, optional log-file rotation, [Sentry](https://github.com/getsentry/sentry-go) for error reporting, and [ttrace](https://github.com/choveylee/ttrace) for trace identifiers in context.

## Features

- Leveled logging (`D` / `I` / `W` / `E` / `F` / `P`) with a single package-level logger initialized at startup
- Optional `detail` field via `Detail` / `Detailf`, and `error` via `Err`
- Trace ID injection when the context carries a valid ID from `ttrace`
- Optional rotating log files (size, time, retention, gzip)
- Error-level lines may be forwarded to Sentry when a DSN is configured

## Installation

```bash
go get github.com/choveylee/tlog
```

## Usage

```go
import (
    "context"

    "github.com/choveylee/tlog"
)

func example(ctx context.Context) {
    tlog.I(ctx).Msg("hello")
    tlog.E(ctx).Err(err).Msg("operation failed")
    tlog.I(ctx).Detailf("user=%d", id).Msgf("request %s", reqID)
}
```

Configuration keys are defined as exported constants (for example `LogLevel`, `SentryDsn`, `LogFileEnable`) and are read through `tcfg` during package initialization.

## Documentation

The package overview and linked references live in [`doc.go`](doc.go). View rendered documentation with:

```bash
go doc -all github.com/choveylee/tlog
```
