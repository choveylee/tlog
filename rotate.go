/**
 * @Author: lidonglin
 * @Description: Size- and time-based log rotation, retention, optional gzip
 * @File:  rotate.go
 * @Version: 1.0.0
 * @Date: 2022/10/12 17:47
 */

package tlog

import (
	"compress/gzip"
	"fmt"
	"io"
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
	// BackupTimeFormat is the reference time layout used in rotated backup file names.
	BackupTimeFormat = "2006_01_02T15_04_05"
	// CompressSuffix is appended to a log path when the rotated file is gzip-compressed.
	CompressSuffix = ".gz"
	// DefaultMaxSize is the maximum log file size in megabytes when fileSize is unset or non-positive.
	DefaultMaxSize = 100
)

var (
	// MegaByte converts megabyte counts to bytes for size limits.
	MegaByte = 1024 * 1024
)

// Compile-time assertion that RotateWriter implements io.WriteCloser.
var _ io.WriteCloser = (*RotateWriter)(nil)

// chown is reserved for copying ownership to new files; non-Linux builds always return nil.
func chown(_ string, _ os.FileInfo) error {
	return nil
}

// getRotateTime normalizes a timestamp to a rotation boundary: local midnight when rotateDuration
// is a multiple of 24 hours, otherwise rotateTime truncated to rotateDuration.
func getRotateTime(rotateTime time.Time, rotateDuration time.Duration) time.Time {
	if rotateDuration%(24*time.Hour) == 0 {
		currentRotateTime := time.Date(rotateTime.Year(), rotateTime.Month(), rotateTime.Day(), 0, 0, 0, 0, time.Local)

		return currentRotateTime
	}

	return rotateTime.Truncate(rotateDuration)
}

// RotateWriter is an io.WriteCloser that writes to a primary log path and rolls the file by time
// and/or by size. Retention and compression are applied asynchronously after rotation.
type RotateWriter struct {
	// filePath is the active log file path; if empty it is derived from tcfg AppName.
	filePath string

	// fileSize is the soft maximum log file size in megabytes before size-based rotation.
	fileSize int

	// fileRotate is the interval between time-based rotations.
	fileRotate time.Duration

	// fileExpired removes rotated backups older than this many days when positive.
	fileExpired int

	// fileCount caps the number of rotated backups retained when positive.
	fileCount int

	// isCompress enables background gzip of rotated files when true.
	isCompress bool

	file *os.File
	size int64

	rotateTime time.Time
	cursor     atomic.Int32

	millChan chan bool

	sync.Mutex
}

// newRotateWriter returns a configured writer. fileRotate is expressed in hours and starts a
// background runMill goroutine for retention and compression.
func newRotateWriter(filePath string, fileSize int, fileRotate, fileExpired, fileCount int, isCompress bool) *RotateWriter {
	rotateWriter := &RotateWriter{
		filePath: filePath,

		fileSize: fileSize,

		fileRotate: time.Duration(fileRotate) * time.Hour,

		fileExpired: fileExpired,
		fileCount:   fileCount,

		isCompress: isCompress,

		millChan: make(chan bool, 1),
	}

	rotateWriter.cursor.Store(-1)

	go rotateWriter.runMill()

	return rotateWriter
}

// Write implements io.Writer. It opens the log file on first use. It performs at most one
// time-based rotation when the interval has elapsed, then at most one size-based rotation when
// appending would exceed getMaxSize, and finally issues a single os.File.Write for the entire buffer.
// A single Write larger than getMaxSize may therefore produce one file larger than the configured cap.
func (p *RotateWriter) Write(data []byte) (int, error) {
	p.Lock()
	defer p.Unlock()

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

// Close implements io.Closer by closing the active log file handle when present.
func (p *RotateWriter) Close() error {
	p.Lock()
	defer p.Unlock()

	return p.close()
}

// close releases the active file descriptor when p.file is non-nil.
func (p *RotateWriter) close() error {
	if p.file == nil {
		return nil
	}

	err := p.file.Close()
	p.file = nil

	return err
}

// rotate closes the current file, renames it to a timestamped backup, opens a new primary file, and notifies runMill.
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
	case p.millChan <- true:
	default:
	}

	return nil
}

// newLogFile creates or truncates the primary log path after renaming any existing file to a backup.
// The caller must have closed the previous handle.
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

// getFilePath returns the configured path, defaulting to "<AppName>.log" from tcfg when unset.
func (p *RotateWriter) getFilePath() string {
	if p.filePath != "" {
		return p.filePath
	}

	appName := tcfg.DefaultString(AppName, "rcrai")

	p.filePath = fmt.Sprintf("%s.log", appName)

	return p.filePath
}

// getRotateFilePath returns the backup filename for the current rotateTime and cursor: prefix.time.cursor.ext.
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

// openLogFile opens or creates the primary log, sets p.size from the file, and initializes rotation state.
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

// runMill processes retention and gzip compression after each rotation signal on millChan.
func (p *RotateWriter) runMill() {
	for range p.millChan {
		if p.fileCount == 0 && p.fileExpired == 0 && p.isCompress == false {
			continue
		}

		historyLogFiles, err := p.getHistoryLogFiles()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tlog rotate: list history log files in %s: %v\n", p.getFileDir(), err)
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
				fmt.Fprintf(os.Stderr, "tlog rotate: remove old log %s: %v\n", path, err)
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
					fmt.Fprintf(os.Stderr, "tlog rotate: compress %s -> %s: %v\n", path, dst, err)
				}
			}
		}
	}
}

// getHistoryLogFiles lists recognizable rotated backups beside the active log file, newest modification time first.
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
			historyLogFile := &logFile{fileInfo.ModTime(), fileInfo}

			historyLogFiles = append(historyLogFiles, historyLogFile)

			continue
		}

		modifyTime = parseTimeByFilename(dirEntry.Name(), prefix, ext+CompressSuffix)
		if modifyTime != nil {
			historyLogFile := &logFile{fileInfo.ModTime(), fileInfo}

			historyLogFiles = append(historyLogFiles, historyLogFile)

			continue
		}
	}

	sort.Sort(logFiles(historyLogFiles))

	return historyLogFiles, nil
}

// parseTimeByFilename extracts a BackupTimeFormat timestamp from filename for the given prefix and suffix, or returns nil.
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

// getMaxSize returns the size threshold in bytes that triggers size-based rotation.
func (p *RotateWriter) getMaxSize() int64 {
	if p.fileSize <= 0 {
		return int64(DefaultMaxSize * MegaByte)
	}

	return int64(p.fileSize) * int64(MegaByte)
}

// getFileDir returns the directory containing the primary log path.
func (p *RotateWriter) getFileDir() string {
	return filepath.Dir(p.filePath)
}

// getPrefixExt returns the basename prefix with a trailing dot and the extension for backup name matching.
func (p *RotateWriter) getPrefixExt() (prefix, ext string) {
	filename := filepath.Base(p.filePath)

	ext = filepath.Ext(filename)
	prefix = filename[:len(filename)-len(ext)] + "."
	return prefix, ext
}

// compressLogFile gzip-compresses src to dst and removes src on success.
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

// getLogFileCursor returns the next backup sequence number for the current time window by reading
// one directory level next to the active log file.
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

// logFile pairs a directory entry with the modification time used for sorting.
type logFile struct {
	modifyTime time.Time

	os.FileInfo
}

// logFiles is a slice sortable by modifyTime for retention ordering.
type logFiles []*logFile

// Less implements sort.Interface so newer modifyTime values sort earlier.
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
