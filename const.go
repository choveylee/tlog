package tlog

// Configuration keys consumed through tcfg during package initialization.
const (
	// AppName is the configuration key for the application name. The configured value is
	// written to the app_name field and used as the default base name for log files.
	AppName = "APP_NAME"

	// LogLevel is the configuration key for the minimum enabled log level.
	LogLevel = "LOG_LEVEL"

	// LogFileEnable is the configuration key that enables output to a rotating log file
	// in addition to standard output.
	LogFileEnable = "LOG_FILE_ENABLE"

	// LogFilePath is the configuration key for the active log file path.
	LogFilePath = "LOG_FILE_PATH"
	// LogFileSize is the configuration key for the maximum active log file size,
	// in megabytes, before size-based rotation occurs.
	LogFileSize = "LOG_FILE_SIZE"

	// LogFileRotate is the configuration key for the time-based rotation interval, in hours.
	LogFileRotate = "LOG_FILE_ROTATE"
	// LogFileExpired is the configuration key for deleting rotated log files older than
	// the specified number of days. A value of zero disables age-based deletion.
	LogFileExpired = "LOG_FILE_EXPIRED"
	// LogFileCount is the configuration key for the maximum number of rotated log files
	// to retain. A value of zero disables count-based deletion.
	LogFileCount = "LOG_FILE_COUNT"

	// LogFileCompress is the configuration key that enables asynchronous gzip compression
	// of rotated log files.
	LogFileCompress = "LOG_FILE_COMPRESS"

	// SentryDsn is the configuration key for the Sentry project DSN. An empty value
	// disables Sentry reporting.
	SentryDsn = "SENTRY_DSN"
)

// Log level names accepted by configuration and by setGlobalLevel.
const (
	LogLevelDebug = "DEBUG"
	LogLevelInfo  = "INFO"
	LogLevelWarn  = "WARN"
	LogLevelError = "ERROR"
	LogLevelFatal = "FATAL"
	LogLevelPanic = "PANIC"
)
