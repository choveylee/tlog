package tlog

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// BackupTimeFormat is the timestamp layout in rotated backup filenames.
	BackupTimeFormat = "2006_01_02T15_04_05"
	// CompressSuffix identifies gzip-compressed backups.
	CompressSuffix = ".gz"
	// DefaultMaxSize preserves the legacy size default in mebibytes. Package logging defaults to 500.
	DefaultMaxSize = 100
	// DefaultRotateHours is the default time-rotation interval in hours.
	DefaultRotateHours = 1

	maxRotationInt64 = int64(^uint64(0) >> 1)
)

// MegaByte is the legacy bytes-per-mebibyte setting. Writers snapshot it during construction.
var MegaByte = 1024 * 1024

var _ io.WriteCloser = (*RotateWriter)(nil)

// RotateWriter owns one active log path. Use one writer/process per path.
// Writes and Close are serialized; maintenance uses immutable configuration and closed backups.
// The zero value rejects writes and can be closed safely.
type RotateWriter struct {
	filePath   string
	maxSize    int64
	fileRotate time.Duration

	fileExpired int
	fileCount   int
	isCompress  bool

	file       *os.File
	size       int64
	rotateTime time.Time
	cursor     int64

	millChan chan struct{}
	done     chan struct{}
	millDone chan struct{}
	closed   bool
	closeErr error

	sync.Mutex
}

// newRotateWriter validates units, resolves the log path against the current working directory,
// and opens the active file without truncation before starting the maintenance worker.
func newRotateWriter(filePath string, fileSize int, fileRotate, fileExpired, fileCount int, isCompress bool) (*RotateWriter, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, errors.New("tlog: log file path must not be empty")
	}

	if MegaByte <= 0 || fileSize <= 0 || int64(fileSize) > maxRotationInt64/int64(MegaByte) {
		return nil, errors.New("tlog: log file size must be positive and fit in an int64 byte count")
	}

	if fileRotate <= 0 || int64(fileRotate) > maxRotationInt64/int64(time.Hour) {
		return nil, errors.New("tlog: log rotation hours must be positive and fit in a time.Duration")
	}

	if fileExpired < 0 || int64(fileExpired) > maxRotationInt64/int64(24*time.Hour) {
		return nil, errors.New("tlog: log expiry days must be non-negative and fit in a time.Duration")
	}

	if fileCount < 0 {
		return nil, errors.New("tlog: log backup count must be non-negative")
	}

	absolutePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("tlog: resolve log file path: %w", err)
	}

	writer := &RotateWriter{
		filePath:   absolutePath,
		maxSize:    int64(fileSize) * int64(MegaByte),
		fileRotate: time.Duration(fileRotate) * time.Hour,

		fileExpired: fileExpired,
		fileCount:   fileCount,
		isCompress:  isCompress,

		millChan: make(chan struct{}, 1),
		done:     make(chan struct{}),
		millDone: make(chan struct{}),
	}

	if err := writer.openLogFile(); err != nil {
		if writer.file != nil {
			_ = writer.file.Close()
		}

		return nil, err
	}

	go writer.runMill()
	writer.millChan <- struct{}{}

	return writer, nil
}

// Write appends a complete record, rotating at most once before the write. Empty active files
// advance to the new window without creating a backup. An oversized record stays in one file.
// Failed or partial writes report the actual number of bytes written.
func (writer *RotateWriter) Write(data []byte) (int, error) {
	if writer == nil {
		return 0, errors.New("tlog: rotate writer must be initialized")
	}

	writer.Lock()
	defer writer.Unlock()

	if writer.closed {
		return 0, os.ErrClosed
	}

	if writer.done == nil || writer.maxSize <= 0 || writer.fileRotate <= 0 {
		return 0, errors.New("tlog: rotate writer must be initialized")
	}

	if len(data) == 0 {
		return 0, nil
	}

	if writer.file == nil {
		if err := writer.openLogFile(); err != nil {
			return 0, err
		}
	}

	currentWindow := getRotateTime(time.Now(), writer.fileRotate)
	timeRotate := !currentWindow.Equal(writer.rotateTime)
	sizeRotate := writer.size > 0 && int64(len(data)) > writer.maxSize-writer.size
	if timeRotate || sizeRotate {
		// An empty archive would consume a retention slot without preserving any records.
		if writer.size > 0 {
			if err := writer.newLogFile(); err != nil {
				return 0, err
			}
		}

		cursor, err := writer.nextCursor(currentWindow)
		if err != nil {
			return 0, err
		}

		writer.rotateTime = currentWindow
		writer.cursor = cursor
		select {
		case writer.millChan <- struct{}{}:
		default:
		}
	}

	written, err := writer.file.Write(data)
	writer.size += int64(written)
	if err != nil {
		return written, fmt.Errorf("tlog: write active log file: %w", err)
	}

	return written, nil
}

// Close closes the active file and waits for final retention/compression. Concurrent callers
// wait for the same worker completion. It does not accept further writes, including after errors.
func (writer *RotateWriter) Close() error {
	if writer == nil {
		return nil
	}

	writer.Lock()
	if !writer.closed {
		writer.closed = true
		if writer.file != nil {
			writer.closeErr = writer.file.Close()
			writer.file = nil
		}

		if writer.done != nil {
			close(writer.done)
		}
	}

	err := writer.closeErr
	millDone := writer.millDone
	writer.Unlock()

	if millDone != nil {
		<-millDone
	}

	if err != nil {
		return fmt.Errorf("tlog: close active log file: %w", err)
	}

	return nil
}

// openLogFile appends to a regular active file, or creates a missing one exclusively.
// Directory/symlink and filesystem errors are returned without renaming or truncating the path.
func (writer *RotateWriter) openLogFile() error {
	if err := os.MkdirAll(filepath.Dir(writer.filePath), 0744); err != nil {
		return fmt.Errorf("tlog: create log directory: %w", err)
	}

	fileInfo, err := os.Lstat(writer.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("tlog: inspect active log file: %w", err)
		}

		if err := writer.newLogFile(); err != nil {
			return err
		}

		fileInfo, err = writer.file.Stat()
		if err != nil {
			return fmt.Errorf("tlog: inspect new log file: %w", err)
		}
	} else {
		if !fileInfo.Mode().IsRegular() {
			return errors.New("tlog: active log path must be a regular file")
		}

		file, err := os.OpenFile(writer.filePath, os.O_APPEND|os.O_WRONLY, fileInfo.Mode().Perm())
		if err != nil {
			return fmt.Errorf("tlog: open active log file: %w", err)
		}

		writer.file = file
	}

	writer.size = fileInfo.Size()
	writer.rotateTime = getRotateTime(fileInfo.ModTime(), writer.fileRotate)
	cursor, err := writer.nextCursor(writer.rotateTime)
	if err != nil {
		return err
	}

	writer.cursor = cursor

	return nil
}

// newLogFile closes the old handle and archives a regular active file under a free sequence.
// A new primary path is created exclusively; unrelated existing files are never truncated.
func (writer *RotateWriter) newLogFile() error {
	if writer.file != nil {
		err := writer.file.Close()
		writer.file = nil
		if err != nil {
			return fmt.Errorf("tlog: close log file before rotation: %w", err)
		}
	}

	mode := os.FileMode(0644)
	fileInfo, err := os.Lstat(writer.filePath)
	if err == nil {
		if !fileInfo.Mode().IsRegular() {
			return errors.New("tlog: active log path must be a regular file")
		}

		mode = fileInfo.Mode().Perm()
		if writer.rotateTime.IsZero() {
			writer.rotateTime = getRotateTime(fileInfo.ModTime(), writer.fileRotate)
			cursor, err := writer.nextCursor(writer.rotateTime)
			if err != nil {
				return err
			}

			writer.cursor = cursor
		}

		fileName := filepath.Base(writer.filePath)
		extension := filepath.Ext(fileName)
		prefix := strings.TrimSuffix(fileName, extension)
		for {
			if writer.cursor <= 0 || writer.cursor == maxRotationInt64 {
				return errors.New("tlog: log backup sequence is exhausted")
			}

			backupName := fmt.Sprintf("%s.%s.%d%s", prefix, writer.rotateTime.Format(BackupTimeFormat), writer.cursor, extension)
			backupPath := filepath.Join(filepath.Dir(writer.filePath), backupName)
			occupied := false
			for _, candidate := range []string{backupPath, backupPath + CompressSuffix} {
				if _, err := os.Lstat(candidate); err == nil {
					occupied = true
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("tlog: inspect log backup path: %w", err)
				}
			}

			if occupied {
				writer.cursor++

				continue
			}

			if err := os.Rename(writer.filePath, backupPath); err != nil {
				return fmt.Errorf("tlog: archive active log file: %w", err)
			}

			break
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("tlog: inspect log file before rotation: %w", err)
	}

	file, err := os.OpenFile(writer.filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("tlog: create active log file: %w", err)
	}

	writer.file = file
	writer.size = 0

	return nil
}

// runMill coalesces notifications and performs a final pass when Close is requested.
// It only touches regular, strictly named closed backups; temporary files and symlinks are ignored.
func (writer *RotateWriter) runMill() {
	defer close(writer.millDone)

	for {
		closing := false
		select {
		case <-writer.done:
			closing = true
		case <-writer.millChan:
		}

		if writer.fileCount == 0 && writer.fileExpired == 0 && !writer.isCompress {
			if closing {
				return
			}

			continue
		}

		historyLogFiles, err := writer.getHistoryLogFiles()
		if err != nil {
			log.Printf("tlog: enumerate log backups: %v", err)
			if closing {
				return
			}

			continue
		}

		preservedLogFileMap := make(map[string]struct{})
		expiryTime := time.Now().AddDate(0, 0, -writer.fileExpired)
		var retainedFiles []*logFile
		for _, historyFile := range historyLogFiles {
			remove := writer.fileExpired > 0 && historyFile.ModTime().Before(expiryTime)
			if !remove {
				baseName := historyFile.Name()
				if historyFile.compressed {
					baseName = strings.TrimSuffix(baseName, CompressSuffix)
				}

				preservedLogFileMap[baseName] = struct{}{}
				remove = writer.fileCount > 0 && len(preservedLogFileMap) > writer.fileCount
			}

			backupPath := filepath.Join(filepath.Dir(writer.filePath), historyFile.Name())
			if remove {
				if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
					log.Printf("tlog: remove log backup: %v", err)
				}

				continue
			}

			retainedFiles = append(retainedFiles, historyFile)
		}

		// Prune first so an expired compressed counterpart cannot block its retained raw source.
		for _, historyFile := range retainedFiles {
			if !writer.isCompress || historyFile.compressed {
				continue
			}

			backupPath := filepath.Join(filepath.Dir(writer.filePath), historyFile.Name())
			if err := compressLogFile(backupPath, backupPath+CompressSuffix); err != nil && !os.IsNotExist(err) && !errors.Is(err, os.ErrExist) {
				log.Printf("tlog: compress log backup: %v", err)
			}
		}

		if closing {
			return
		}
	}
}

// getHistoryLogFiles parses complete timestamp/positive-sequence names and sorts by window,
// descending numeric sequence, then name. Raw/gzip counterparts remain adjacent for retention.
func (writer *RotateWriter) getHistoryLogFiles() ([]*logFile, error) {
	dirEntries, err := os.ReadDir(filepath.Dir(writer.filePath))
	if err != nil {
		return nil, fmt.Errorf("tlog: read log directory: %w", err)
	}

	fileName := filepath.Base(writer.filePath)
	extension := filepath.Ext(fileName)
	prefix := strings.TrimSuffix(fileName, extension) + "."
	var historyLogFiles []*logFile
	for _, entry := range dirEntries {
		if entry.Name() == fileName || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}

		fileInfo, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, fmt.Errorf("tlog: inspect log backup: %w", err)
		}

		if !fileInfo.Mode().IsRegular() {
			continue
		}

		for _, suffix := range []string{extension, extension + CompressSuffix} {
			if !strings.HasSuffix(entry.Name(), suffix) || len(entry.Name()) <= len(prefix)+len(suffix) {
				continue
			}

			encoded := entry.Name()[len(prefix) : len(entry.Name())-len(suffix)]
			separator := strings.LastIndexByte(encoded, '.')
			if separator <= 0 {
				continue
			}

			cursorText := encoded[separator+1:]
			cursor, err := strconv.ParseInt(cursorText, 10, 64)
			if err != nil || cursor <= 0 || strconv.FormatInt(cursor, 10) != cursorText {
				continue
			}

			stamp, err := time.ParseInLocation(BackupTimeFormat, encoded[:separator], time.Local)
			if err != nil {
				continue
			}

			historyFile := &logFile{modifyTime: stamp, cursor: cursor, compressed: suffix != extension, FileInfo: fileInfo}

			historyLogFiles = append(historyLogFiles, historyFile)

			break
		}
	}

	sort.Slice(historyLogFiles, func(leftIndex, rightIndex int) bool {
		left, right := historyLogFiles[leftIndex], historyLogFiles[rightIndex]
		if !left.modifyTime.Equal(right.modifyTime) {
			return left.modifyTime.After(right.modifyTime)
		}

		if left.cursor != right.cursor {
			return left.cursor > right.cursor
		}

		return left.Name() < right.Name()
	})

	return historyLogFiles, nil
}

// nextCursor includes compressed and extensionless backups, grouping ambiguous DST wall-clock
// labels together so repeated hours cannot reuse an existing filename.
func (writer *RotateWriter) nextCursor(window time.Time) (int64, error) {
	history, err := writer.getHistoryLogFiles()
	if err != nil {
		return 0, err
	}

	var maxCursor int64
	for _, file := range history {
		if file.modifyTime.Format(BackupTimeFormat) == window.Format(BackupTimeFormat) && file.cursor > maxCursor {
			maxCursor = file.cursor
		}
	}

	if maxCursor == maxRotationInt64 {
		return 0, errors.New("tlog: log backup sequence is exhausted")
	}

	return maxCursor + 1, nil
}

// getRotateTime aligns day multiples to stable local calendar-day buckets. Intraday divisors
// of 24h start at local midnight; other durations keep absolute-time truncation semantics.
func getRotateTime(rotateTime time.Time, rotateDuration time.Duration) time.Time {
	if rotateDuration <= 0 {
		return rotateTime
	}

	localTime := rotateTime.In(time.Local)
	midnight := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 0, 0, 0, 0, time.Local)
	if rotateDuration%(24*time.Hour) == 0 {
		periodDays := int64(rotateDuration / (24 * time.Hour))
		civilDay := time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400
		offset := civilDay % periodDays
		if offset < 0 {
			offset += periodDays
		}

		return midnight.AddDate(0, 0, -int(offset))
	}

	if (24*time.Hour)%rotateDuration == 0 {
		return midnight.Add(localTime.Sub(midnight).Truncate(rotateDuration))
	}

	return rotateTime.Truncate(rotateDuration)
}

// compressLogFile publishes a completed gzip through a same-directory temporary file.
// Existing destinations are preserved; source data is removed only after all closes and rename succeed.
func compressLogFile(sourcePath, destinationPath string) error {
	fileInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return fmt.Errorf("tlog: inspect compression source: %w", err)
	}

	if !fileInfo.Mode().IsRegular() {
		return errors.New("tlog: compression source must be a regular file")
	}

	if _, err := os.Lstat(destinationPath); err == nil {
		return fmt.Errorf("tlog: compression destination already exists: %w", os.ErrExist)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("tlog: inspect compression destination: %w", err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("tlog: open compression source: %w", err)
	}

	defer source.Close()

	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".tlog-compress-*")
	if err != nil {
		return fmt.Errorf("tlog: create temporary gzip file: %w", err)
	}

	defer os.Remove(temporary.Name())
	defer temporary.Close()

	if err := temporary.Chmod(fileInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("tlog: set gzip file permissions: %w", err)
	}

	gzipWriter := gzip.NewWriter(temporary)
	if _, err := io.Copy(gzipWriter, source); err != nil {
		_ = gzipWriter.Close()

		return fmt.Errorf("tlog: compress log data: %w", err)
	}

	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("tlog: finish gzip stream: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("tlog: close gzip file: %w", err)
	}

	if err := source.Close(); err != nil {
		return fmt.Errorf("tlog: close compression source: %w", err)
	}

	// Retention follows the newest content in the source, not the time spent compressing it.
	if err := os.Chtimes(temporary.Name(), fileInfo.ModTime(), fileInfo.ModTime()); err != nil {
		return fmt.Errorf("tlog: preserve compressed log timestamps: %w", err)
	}

	if err := os.Rename(temporary.Name(), destinationPath); err != nil {
		return fmt.Errorf("tlog: publish gzip file: %w", err)
	}

	if err := os.Remove(sourcePath); err != nil {
		return fmt.Errorf("tlog: remove compressed source: %w", err)
	}

	return nil
}

// logFile stores parsed backup metadata independently of the filename extension.
type logFile struct {
	modifyTime time.Time
	cursor     int64
	compressed bool

	os.FileInfo
}
