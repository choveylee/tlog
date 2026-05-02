# tlog

`tlog` provides structured logging for Go applications on top of [zerolog](https://github.com/rs/zerolog). It integrates with [tcfg](https://github.com/choveylee/tcfg) for configuration, [ttrace](https://github.com/choveylee/ttrace) for trace propagation, optional rotating file output, and optional [Sentry](https://github.com/getsentry/sentry-go) reporting for error-level events.

## Capabilities

- Process-wide default logger configured during package initialization
- Structured leveled logging through `D`, `I`, `W`, `E`, `F`, and `P`
- Optional `detail` and `error` fields through `Detail`, `Detailf`, and `Err`
- Automatic `trace_id` injection from `context.Context` when a valid `ttrace` identifier is present
- Optional size-based and time-based file rotation with retention and gzip compression
- Optional forwarding of error-level log entries to Sentry

## Installation

```bash
go get github.com/choveylee/tlog
```

## Example

```go
package main

import (
	"context"

	"github.com/choveylee/tlog"
)

func handleRequest(ctx context.Context, requestID string, userID int, err error) {
	tlog.I(ctx).Msg("service startup completed successfully")
	tlog.I(ctx).Detailf("user_id=%d", userID).Msgf("request %s has been accepted for processing", requestID)

	if err != nil {
		tlog.E(ctx).Err(err).Msg("request processing failed")
	}
}
```

## Configuration

`tlog` reads configuration during package initialization through `tcfg`. Exported constants such as `AppName`, `LogLevel`, `LogFileEnable`, `LogFilePath`, and `SentryDsn` define the supported keys. Use `tcfg.LocalKey` when environment-specific scoping is required.

Common configuration keys include the following:

- `AppName`
- `LogLevel`
- `LogFileEnable`
- `LogFilePath`
- `LogFileSize`
- `LogFileRotate`
- `LogFileExpired`
- `LogFileCount`
- `LogFileCompress`
- `SentryDsn`

## Documentation

See [`doc.go`](doc.go) for the package overview and rendered API references. To inspect the package locally, run:

```bash
go doc -all github.com/choveylee/tlog
```
