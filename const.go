package tlog

// Configuration keys for tcfg (use with tcfg.LocalKey). Values are read during package initialization.
const (
	// AppName is the configuration key for the application name (zerolog field app_name and default log basename).
	AppName = "APP_NAME"

	// LogLevel is the configuration key for the minimum enabled log level (see LogLevelDebug and siblings).
	LogLevel = "LOG_LEVEL"

	// LogFileEnable is the configuration key that enables writing to a rotating log file in addition to stdout.
	LogFileEnable = "LOG_FILE_ENABLE"

	// LogFilePath is the configuration key for the primary log file path.
	LogFilePath = "LOG_FILE_PATH"
	// LogFileSize is the configuration key for the maximum log file size in megabytes before size-based rotation.
	LogFileSize = "LOG_FILE_SIZE"

	// LogFileRotate is the configuration key for the time-based rotation interval, in hours.
	LogFileRotate = "LOG_FILE_ROTATE"
	// LogFileExpired is the configuration key for deleting rotated files older than this many days; zero disables.
	LogFileExpired = "LOG_FILE_EXPIRED"
	// LogFileCount is the configuration key for the maximum number of rotated files to retain; zero disables.
	LogFileCount = "LOG_FILE_COUNT"

	// LogFileCompress is the configuration key that enables asynchronous gzip compression of rotated files.
	LogFileCompress = "LOG_FILE_COMPRESS"

	// SentryDsn is the configuration key for the Sentry project DSN; empty disables Sentry.
	SentryDsn = "SENTRY_DSN"
)

// Log level name strings accepted by configuration and by setGlobalLevel.
const (
	LogLevelDebug = "DEBUG"
	LogLevelInfo  = "INFO"
	LogLevelWarn  = "WARN"
	LogLevelError = "ERROR"
	LogLevelFatal = "FATAL"
	LogLevelPanic = "PANIC"
)
