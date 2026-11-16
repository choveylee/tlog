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

Go 1.27.4 or later is required.

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

func handleRequest(ctx context.Context, requestId string, userId int, err error) {
	tlog.I(ctx).Msg("service startup completed successfully")
	tlog.I(ctx).Detailf("user_id=%d", userId).Msgf("request %s has been accepted for processing", requestId)

	if err != nil {
		tlog.E(ctx).Err(err).Msg("request processing failed")
	}
}
```

Stop log producers before calling `tlog.Shutdown()` on process exit. It closes the owned log file,
waits for pending retention/compression, and flushes Sentry with a two-second timeout. Repeated
calls are safe and standard output remains open. Zerolog's fatal cleanup uses the same path before
exiting; a panic flushes Sentry while allowing recovered applications to continue logging.
Tracing shutdown remains the application's responsibility through `ttrace`.

Each event is single-use and must not be copied or shared between goroutines. `Msg` and `Msgf`
return the message even when filtering suppresses output. Detail fragments share a 10000-byte
retained-data budget, preserve UTF-8 boundaries when truncated, and append `...` when content is
discarded. `Detailf` formats before truncation; arbitrary formatters may allocate their own buffers.

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

File logging defaults to a 500 MiB soft limit and a one-hour rotation interval. Size and rotation
hours must be positive and fit their byte/duration representations; expiry days and retained-backup
counts must be non-negative. Empty paths and non-regular active files are rejected. Invalid file
configuration is reported to standard error and leaves stdout/Sentry logging available.

Use one writer/process per active path. Relative paths are resolved against the working directory
at initialization. Records larger than the size limit stay intact in one file. An empty active file
crossing a time window is reused, leaving backup slots for files that contain log records.
Backups are ordered by their timestamp and numeric sequence, including names without an extension.
Raw/gzip counterparts count as one retained backup. Malformed names, directories, symlinks, and
temporary compression files are excluded from retention. Existing backups are preserved across
restart. Age-based deletion uses the file's last write time, preserved through compression, so a
long window's starting timestamp cannot expire its recent contents early. Day-multiple intervals
use stable local calendar-day boundaries; intervals dividing 24h
align to local midnight, while other durations use absolute-time truncation.

Compression writes to a temporary file in the same directory and publishes the completed gzip
before removing its source. Existing gzip destinations are preserved. Interrupted temporary files
are ignored by retention and can be removed while the application is stopped.

Sentry client construction completes locally before logging starts; it does not probe the remote
service. Invalid DSNs disable forwarding without logging credentials. Error records use Sentry's
error severity; fatal/panic records use fatal severity through isolated scopes. Plain `Write` calls
to a zero-value `SentryWriter` safely discard data; an explicitly supplied embedded writer handles
unlevelled data. `WriteLevel` forwards only error/fatal/panic events.

## Validation

```bash
gofmt -l *.go
go build ./...
go vet ./...
go mod verify
```

## Documentation

See [`doc.go`](doc.go) for the package overview and rendered API references. To inspect the package locally, run:

```bash
go doc -all github.com/choveylee/tlog
```
