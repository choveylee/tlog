/**
 * @Author: lidonglin
 * @Description: tcfg key names and log-level string constants
 * @File:  const.go
 * @Version: 1.0.0
 * @Date: 2022/10/12 17:47
 */

package tlog

// Keys for tcfg lookups (use with tcfg.LocalKey). Values configure the package-level logger at init.
const (
	// AppName is the tcfg key for the application display name (zerolog field app_name and default log basename).
	AppName = "APP_NAME"

	// LogLevel is the tcfg key for the minimum enabled log level (see LogLevel* constants).
	LogLevel = "LOG_LEVEL"

	// LogFileEnable is the tcfg key; when true, output is also written to a rotating file.
	LogFileEnable = "LOG_FILE_ENABLE"

	// LogFilePath is the tcfg key for the active log file path.
	LogFilePath = "LOG_FILE_PATH"
	// LogFileSize is the tcfg key for the maximum log file size in megabytes before size-based rotation.
	LogFileSize = "LOG_FILE_SIZE"

	// LogFileRotate is the tcfg key for the time-based rotation interval, in hours.
	LogFileRotate = "LOG_FILE_ROTATE"
	// LogFileExpired is the tcfg key for deleting rotated files older than this many days (0 disables).
	LogFileExpired = "LOG_FILE_EXPIRED"
	// LogFileCount is the tcfg key for the maximum number of rotated files to retain (0 disables).
	LogFileCount = "LOG_FILE_COUNT"

	// LogFileCompress is the tcfg key; when true, rotated files are gzip-compressed in the background.
	LogFileCompress = "LOG_FILE_COMPRESS"

	// SentryDsn is the tcfg key for the Sentry project DSN; empty disables Sentry integration.
	SentryDsn = "SENTRY_DSN"
)

// Log level strings accepted by tcfg and mapped in setGlobalLevel.
const (
	LogLevelDebug = "DEBUG"
	LogLevelInfo  = "INFO"
	LogLevelWarn  = "WARN"
	LogLevelError = "ERROR"
	LogLevelFatal = "FATAL"
	LogLevelPanic = "PANIC"
)
