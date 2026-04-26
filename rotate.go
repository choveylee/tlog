package tlog

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/choveylee/tcfg"
)

const (
	// BackupTimeFormat is the Go reference time layout embedded in rotated backup filenames.
	BackupTimeFormat = "2006_01_02T15_04_05"
	// CompressSuffix is the filename suffix for gzip-compressed rotated logs.
	CompressSuffix = ".gz"
	// DefaultMaxSize is the default maximum log file size in megabytes when fileSize is unset or non-positive.
	DefaultMaxSize = 100
	// DefaultRotateHours is the fallback time-rotation interval, in hours, when configuration is unset or invalid.
	DefaultRotateHours = 1
)

var (
	// MegaByte is the number of bytes in one mebibyte, used to convert megabyte settings to bytes.
	MegaByte = 1024 * 1024
)

// Compile-time check that RotateWriter implements io.WriteCloser.
var _ io.WriteCloser = (*RotateWriter)(nil)

// chown is a hook for preserving file ownership on some platforms; default builds return nil.
func chown(_ string, _ os.FileInfo) error {
	return nil
}

// getRotateTime aligns t to a rotation boundary: local midnight when rotateDuration divides 24h,
// otherwise t truncated to rotateDuration.
func getRotateTime(rotateTime time.Time, rotateDuration time.Duration) time.Time {
	if rotateDuration <= 0 {
		return rotateTime
	}

	if rotateDuration%(24*time.Hour) == 0 {
		currentRotateTime := time.Date(rotateTime.Year(), rotateTime.Month(), rotateTime.Day(), 0, 0, 0, 0, time.Local)

		return currentRotateTime
	}

	return rotateTime.Truncate(rotateDuration)
}

// RotateWriter implements io.WriteCloser for a primary log file with optional time- and size-based
// rotation, asynchronous retention, and optional gzip of backups.
type RotateWriter struct {
	// filePath is the active log path; empty means derive from tcfg AppName.
	filePath string

	// fileSize is the soft size limit in megabytes before size-based rotation.
	fileSize int

	// fileRotate is the interval between time-based rotations.
	fileRotate time.Duration

	// fileExpired deletes backups older than this many days when positive.
	fileExpired int

	// fileCount limits how many rotated backups to keep when positive.
	fileCount int

	// isCompress enables background gzip compression of rotated files.
	isCompress bool

	file *os.File
	size int64

	rotateTime time.Time
	cursor     atomic.Int32

	millChan chan bool

	done     chan struct{}
	millDone chan struct{}

	closed bool

	sync.Mutex
}

// newRotateWriter constructs a RotateWriter. fileRotate is in hours; it starts runMill for retention.
func newRotateWriter(filePath string, fileSize int, fileRotate, fileExpired, fileCount int, isCompress bool) *RotateWriter {
	if fileRotate <= 0 {
		fileRotate = DefaultRotateHours
	}

	rotateWriter := &RotateWriter{
		filePath: filePath,

		fileSize: fileSize,

		fileRotate: time.Duration(fileRotate) * time.Hour,

		fileExpired: fileExpired,
		fileCount:   fileCount,

		isCompress: isCompress,

		millChan: make(chan bool, 1),

		done:     make(chan struct{}),
		millDone: make(chan struct{}),
	}

	rotateWriter.cursor.Store(-1)

	go rotateWriter.runMill()

	return rotateWriter
}

// Write implements io.Writer. It opens the file on first use, applies at most one time-based
// rotation when due, then at most one size-based rotation when the write would exceed getMaxSize,
// and writes the full buffer in one system call. A buffer larger than getMaxSize may leave one
// file larger than the configured limit.
func (p *RotateWriter) Write(data []byte) (int, error) {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return 0, os.ErrClosed
	}

	if p.file == nil {
		if err := p.openLogFile(); err != nil {
			return 0, err
		}
	}

	curTime := time.Now()
	nextRotateTime := p.rotateTime.Add(p.fileRotate)

	if curTime.Unix() >= nextRotateTime.Unix() {
		if err := p.rotate(); err != nil {
			return 0, err
		}

		p.rotateTime = getRotateTime(curTime, p.fileRotate)
		p.cursor.Store(1)
	}

	writeLen := int64(len(data))
	if p.size+writeLen > p.getMaxSize() {
		if err := p.rotate(); err != nil {
			return 0, err
		}

		p.cursor.Add(1)
	}

	n, err := p.file.Write(data)
	if err != nil {
		return n, err
	}

	p.size += int64(n)

	return n, nil
}

// Close implements io.Closer by closing the active log file if open.
func (p *RotateWriter) Close() error {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true
	close(p.done)

	return p.close()
}

// close closes p.file and clears the field when non-nil.
func (p *RotateWriter) close() error {
	if p.file == nil {
		return nil
	}

	err := p.file.Close()
	p.file = nil

	return err
}

// rotate closes the active file, renames it to a timestamped backup, creates a new primary file, and signals runMill.
func (p *RotateWriter) rotate() error {
	err := p.close()
	if err != nil {
		return err
	}

	err = p.newLogFile()
	if err != nil {
		return err
	}

	select {
	case <-p.done:
	case p.millChan <- true:
	default:
	}

	return nil
}

// newLogFile renames an existing primary file to a backup when present, then creates or truncates
// the primary path. The previous file handle must already be closed.
func (p *RotateWriter) newLogFile() error {
	err := os.MkdirAll(p.getFileDir(), 0744)
	if err != nil {
		return err
	}

	filePath := p.getFilePath()
	mode := os.FileMode(0644)

	fileInfo, err := os.Stat(filePath)
	if err == nil {
		// copy the mode off the old log file.
		mode = fileInfo.Mode()

		rotateFilePath := p.getRotateFilePath()

		err := os.Rename(filePath, rotateFilePath)
		if err != nil {
			return err
		}

		// this is a no-op anywhere but linux
		if err := chown(filePath, fileInfo); err != nil {
			return err
		}
	}

	// we use truncate here because this should only get called when we've moved
	// the file ourselves. if someone else creates the file in the meantime,
	// just wipe out the contents.
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	p.file = file
	p.size = 0

	return nil
}

// getFilePath returns filePath, or resolves and caches "<AppName>.log" from tcfg when empty.
func (p *RotateWriter) getFilePath() string {
	if p.filePath != "" {
		return p.filePath
	}

	appName := tcfg.DefaultString(AppName, "rcrai")

	p.filePath = fmt.Sprintf("%s.log", appName)

	return p.filePath
}

// getRotateFilePath builds the backup path as prefix.timeStr.cursor.ext under the log directory.
func (p *RotateWriter) getRotateFilePath() string {
	filePath := p.filePath

	dir := filepath.Dir(filePath)

	filename := filepath.Base(filePath)
	ext := filepath.Ext(filename)
	prefix := filename[:len(filename)-len(ext)]

	destFilename := ""

	timeStr := p.rotateTime.Format(BackupTimeFormat)
	destFilename = fmt.Sprintf("%s.%s.%d%s", prefix, timeStr, p.cursor.Load(), ext)

	return filepath.Join(dir, destFilename)
}

// openLogFile opens the primary log for append when it exists, or creates it, then sets size and rotation state.
func (p *RotateWriter) openLogFile() error {
	// init file path
	filePath := p.getFilePath()

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) == false {
			return err
		}

		err := p.newLogFile()
		if err != nil {
			return err
		}

		fileInfo, err = os.Stat(filePath)
		if err != nil {
			return err
		}
	} else {
		file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			// if we fail to open the old log file for some reason, just ignore
			// it and open a new log file.
			err := p.newLogFile()
			if err != nil {
				return err
			}
		} else {
			p.file = file
			p.size = fileInfo.Size()
		}
	}

	p.rotateTime = getRotateTime(fileInfo.ModTime(), p.fileRotate)

	cursor := getLogFileCursor(filePath, p.fileRotate)

	p.cursor.Store(cursor)

	return nil
}

// runMill runs in a background goroutine to prune and optionally compress rotated logs after rotation.
func (p *RotateWriter) runMill() {
	defer close(p.millDone)

	for {
		select {
		case <-p.done:
			return
		case <-p.millChan:
		}

		if p.fileCount == 0 && p.fileExpired == 0 && p.isCompress == false {
			continue
		}

		historyLogFiles, err := p.getHistoryLogFiles()
		if err != nil {
			log.Printf("tlog rotate: failed to enumerate rotated log files in %q: %v", p.getFileDir(), err)
			continue
		}

		var removeLogFiles []*logFile

		if p.fileCount > 0 && p.fileCount < len(historyLogFiles) {
			preservedLogFiles := make(map[string]bool)

			var remainLogFiles []*logFile

			for _, historyLogFile := range historyLogFiles {
				// only count the uncompressed log file or the compressed log file, not both.
				filename := historyLogFile.Name()

				filename = strings.TrimSuffix(filename, CompressSuffix)

				preservedLogFiles[filename] = true

				if len(preservedLogFiles) > p.fileCount {
					removeLogFiles = append(removeLogFiles, historyLogFile)
				} else {
					remainLogFiles = append(remainLogFiles, historyLogFile)
				}
			}

			historyLogFiles = remainLogFiles
		}

		if p.fileExpired > 0 {
			expiredDuration := time.Duration(int64(24*time.Hour) * int64(p.fileExpired))
			expiredTime := time.Now().Add(-1 * expiredDuration)

			var remainLogFiles []*logFile

			for _, historyLogFile := range historyLogFiles {
				if historyLogFile.modifyTime.Before(expiredTime) {
					removeLogFiles = append(removeLogFiles, historyLogFile)
				} else {
					remainLogFiles = append(remainLogFiles, historyLogFile)
				}
			}

			historyLogFiles = remainLogFiles
		}

		for _, removeLogFile := range removeLogFiles {
			path := filepath.Join(p.getFileDir(), removeLogFile.Name())
			if err := os.Remove(path); err != nil {
				log.Printf("tlog rotate: failed to remove rotated log file %q: %v", path, err)
			}
		}

		if p.isCompress == true {
			for _, historyLogFile := range historyLogFiles {
				path := filepath.Join(p.getFileDir(), historyLogFile.Name())

				if strings.HasSuffix(path, CompressSuffix) {
					continue
				}

				dst := path + CompressSuffix
				if err := compressLogFile(path, dst); err != nil {
					log.Printf(
						"tlog rotate: failed to compress rotated log file from %q to %q: %v",
						path,
						dst,
						err,
					)
				}
			}
		}
	}
}

// getHistoryLogFiles returns rotated backup entries in the log directory, newest first by backup timestamp.
func (p *RotateWriter) getHistoryLogFiles() ([]*logFile, error) {
	dirEntries, err := os.ReadDir(p.getFileDir())
	if err != nil {
		return nil, err
	}

	var historyLogFiles []*logFile

	prefix, ext := p.getPrefixExt()

	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue
		}

		// file name equals to p.filePath, ignore it
		if filepath.Base(p.filePath) == dirEntry.Name() {
			continue
		}

		fileInfo, err := dirEntry.Info()
		if err != nil {
			continue
		}

		modifyTime := parseTimeByFilename(dirEntry.Name(), prefix, ext)
		if modifyTime != nil {
			historyLogFile := &logFile{*modifyTime, fileInfo}

			historyLogFiles = append(historyLogFiles, historyLogFile)

			continue
		}

		modifyTime = parseTimeByFilename(dirEntry.Name(), prefix, ext+CompressSuffix)
		if modifyTime != nil {
			historyLogFile := &logFile{*modifyTime, fileInfo}

			historyLogFiles = append(historyLogFiles, historyLogFile)

			continue
		}
	}

	sort.Sort(logFiles(historyLogFiles))

	return historyLogFiles, nil
}

// parseTimeByFilename parses a BackupTimeFormat timestamp from filename given prefix and suffix, or returns nil.
func parseTimeByFilename(filename, prefix, ext string) *time.Time {
	if strings.HasPrefix(filename, prefix) == false {
		return nil
	}

	if strings.HasSuffix(filename, ext) == false {
		return nil
	}

	ts := filename[len(prefix) : len(filename)-len(ext)]

	if strings.Contains(ts, ".") {
		index := strings.Index(ts, ".")
		ts = ts[:index]
	}

	updateTime, err := time.ParseInLocation(BackupTimeFormat, ts, time.Local)
	if err != nil {
		return nil
	}

	return &updateTime
}

// getMaxSize returns the configured maximum active file size in bytes before size-based rotation.
func (p *RotateWriter) getMaxSize() int64 {
	if p.fileSize <= 0 {
		return int64(DefaultMaxSize * MegaByte)
	}

	return int64(p.fileSize) * int64(MegaByte)
}

// getFileDir returns the directory name of the primary log path.
func (p *RotateWriter) getFileDir() string {
	return filepath.Dir(p.filePath)
}

// getPrefixExt returns the basename prefix (with trailing dot) and extension for matching backups.
func (p *RotateWriter) getPrefixExt() (prefix, ext string) {
	filename := filepath.Base(p.filePath)

	ext = filepath.Ext(filename)
	prefix = filename[:len(filename)-len(ext)] + "."
	return prefix, ext
}

// compressLogFile writes the gzip of src to dst and removes src when complete.
func compressLogFile(src, dst string) (err error) {
	file, err := os.Open(src)
	if err != nil {
		return err
	}

	defer file.Close()

	fileInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := chown(dst, fileInfo); err != nil {
		return err
	}

	// if this file already exists, we presume it was created by a previous attempt to compress the log file.
	gzFile, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileInfo.Mode())
	if err != nil {
		return err
	}

	defer gzFile.Close()

	gzWriter := gzip.NewWriter(gzFile)

	defer func() {
		if err != nil {
			os.Remove(dst)
		}
	}()

	_, err = io.Copy(gzWriter, file)
	if err != nil {
		return err
	}

	err = gzWriter.Close()
	if err != nil {
		return err
	}

	err = gzFile.Close()
	if err != nil {
		return err
	}

	err = file.Close()
	if err != nil {
		return err
	}

	err = os.Remove(src)
	if err != nil {
		return err
	}

	return nil
}

// getLogFileCursor returns the next backup sequence index for the current rotation window by scanning
// the log directory (non-recursive).
func getLogFileCursor(filePath string, rotateDuration time.Duration) int32 {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 1
		}

		return -1
	}

	modifyTime := fileInfo.ModTime()

	filename := filepath.Base(filePath)

	ext := filepath.Ext(filename)
	prefix := filename[:len(filename)-len(ext)]

	rotateTime := getRotateTime(modifyTime, rotateDuration).Format(BackupTimeFormat)
	rotatePrefix := fmt.Sprintf("%s.%s", prefix, rotateTime)

	fileDir := filepath.Dir(filePath)

	expectPrefix := rotatePrefix + "."

	var maxCursor int64

	dirEntries, err := os.ReadDir(fileDir)
	if err != nil {
		return 1
	}

	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() {
			continue
		}

		tmpFileName := dirEntry.Name()
		if !strings.HasPrefix(tmpFileName, expectPrefix) {
			continue
		}

		subStr := tmpFileName[len(expectPrefix):]
		if subStr == "" {
			continue
		}

		index := strings.Index(subStr, ".")
		if index <= 0 {
			continue
		}

		cursor, err := strconv.ParseInt(subStr[:index], 10, 32)
		if err == nil && cursor > maxCursor {
			maxCursor = cursor
		}
	}

	return int32(maxCursor) + 1
}

// logFile associates filesystem metadata with a modification time used for retention ordering.
type logFile struct {
	modifyTime time.Time

	os.FileInfo
}

// logFiles is a sortable slice of logFile by modifyTime.
type logFiles []*logFile

// Less implements sort.Interface (newer modifyTime sorts before older).
func (f logFiles) Less(i, j int) bool {
	return f[i].modifyTime.After(f[j].modifyTime)
}

// Swap implements sort.Interface.
func (f logFiles) Swap(i, j int) {
	f[i], f[j] = f[j], f[i]
}

// Len implements sort.Interface.
func (f logFiles) Len() int {
	return len(f)
}
