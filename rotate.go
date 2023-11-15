/**
 * @Author: lidonglin
 * @Description:
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
	"time"

	"github.com/choveylee/tcfg"
	"go.uber.org/atomic"
)

const (
	BackupTimeFormat = "2006_01_02T15_04_05"
	CompressSuffix   = ".gz"
	DefaultMaxSize   = 100
)

var (
	// MegaByte is the conversion factor between fileSize and bytes.
	MegaByte = 1024 * 1024
)

// ensure we always implement io.WriteCloser
var _ io.WriteCloser = (*RotateWriter)(nil)

func chown(_ string, _ os.FileInfo) error {
	return nil
}

func getRotateTime(rotateTime time.Time, rotateDuration time.Duration) time.Time {
	if rotateDuration%(24*time.Hour) == 0 {
		currentRotateTime := time.Date(rotateTime.Year(), rotateTime.Month(), rotateTime.Day(), 0, 0, 0, 0, time.Local)

		return currentRotateTime
	}

	return rotateTime.Truncate(rotateDuration)
}

// RotateWriter is an io.WriteCloser that writes to the specified getCurrentFilePath.
// If fileCount and fileExpired are both 0, no old log files will be deleted.
type RotateWriter struct {
	// filePath is the file to write logs to
	filePath string

	// the max size of log file (MB)
	fileSize int

	fileRotate time.Duration

	// max day to retain history log files
	fileExpired int

	// max count to retain history log files
	fileCount int

	// determine if the rotated log files be compressed
	isCompress bool

	file *os.File
	size int64

	rotateTime time.Time
	cursor     *atomic.Int32

	millChan chan bool

	sync.Mutex
}

func newRotateWriter(filePath string, fileSize int, fileRotate, fileExpired, fileCount int, isCompress bool) *RotateWriter {
	rotateWriter := &RotateWriter{
		filePath: filePath,

		fileSize: fileSize,

		fileRotate: time.Duration(fileRotate) * time.Hour,

		fileExpired: fileExpired,
		fileCount:   fileCount,

		isCompress: isCompress,

		cursor: atomic.NewInt32(-1),

		millChan: make(chan bool, 1),
	}

	go rotateWriter.runMill()

	return rotateWriter
}

// Write implements io.Writer.  If a write would cause the log file to be larger
// than fileSize, the file is closed, renamed to include a modifyTime of the
// current time, and a new log file is created using the original log file name.
// If the length of the write is greater than fileSize, an error is returned.
func (p *RotateWriter) Write(data []byte) (int, error) {
	p.Lock()
	defer p.Unlock()

	if p.file == nil {
		err := p.openLogFile()
		if err != nil {
			return 0, err
		}
	}

	rotateTime := p.rotateTime

	nextRotateTime := rotateTime.Add(p.fileRotate)

	curTime := time.Now()

	if curTime.Unix() >= nextRotateTime.Unix() {
		err := p.rotate()
		if err != nil {
			return 0, err
		}

		p.rotateTime = getRotateTime(curTime, p.fileRotate)
		p.cursor.Store(1)
	}

	writeLen := int64(len(data))

	// over the getMaxSize size about this log file
	if p.size+writeLen > p.getMaxSize() {
		err := p.rotate()
		if err != nil {
			return 0, err
		}

		p.cursor.Add(1)
	}

	n, err := p.file.Write(data)
	if err != nil {
		return 0, err
	}

	p.size += int64(n)

	return n, nil
}

// Close implements io.Closer, and closes the current confFile.
func (p *RotateWriter) Close() error {
	p.Lock()
	defer p.Unlock()

	return p.close()
}

// close closes the file if it is open.
func (p *RotateWriter) close() error {
	if p.file == nil {
		return nil
	}

	err := p.file.Close()
	p.file = nil

	return err
}

// rotate closes the current file, moves it aside with a modifyTime in the name,
// (if it exists), opens a new file with the original getCurrentFilePath, and then runs
// post-rotation processing and removal.
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

// newLogFile opens a new log file for writing, moving any old log file out of the
// way.  This methods assumes the file has already been closed.
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

func (p *RotateWriter) getFilePath() string {
	if p.filePath != "" {
		return p.filePath
	}

	appName := tcfg.DefaultString(AppName, "rcrai")

	p.filePath = fmt.Sprintf("%s.log", appName)

	return p.filePath
}

// getRotateFilePath generates the name of the confFile from the current time.
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

// openLogFile opens the confFile if it exists and if the current write
// would not put it over fileSize.  If there is no such file or the write would
// put it over the fileSize, a new file is created.
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

// runMill runs in a goroutine to manage post-rotation compression and removal
// of old log files.
func (p *RotateWriter) runMill() {
	for range p.millChan {
		if p.fileCount == 0 && p.fileExpired == 0 && p.isCompress == false {
			continue
		}

		historyLogFiles, err := p.getHistoryLogFiles()
		if err != nil {
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
			err := os.Remove(filepath.Join(p.getFileDir(), removeLogFile.Name()))
			if err != nil {
				// TODO
			}
		}

		if p.isCompress == true {
			for _, f := range historyLogFiles {
				fn := filepath.Join(p.getFileDir(), f.Name())

				if strings.HasSuffix(fn, CompressSuffix) {
					continue
				}

				err := compressLogFile(fn, fn+CompressSuffix)
				if err != nil {
					// TODO
				}
			}
		}
	}
}

// getHistoryLogFiles returns the list of backup log files stored in the same
// directory as the current log file, sorted by ModTime
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

// parseTimeByFilename extracts the formatted time from the path by stripping off path's prefix and extension
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

// getMaxSize returns the maximum size in bytes of log files before rolling.
func (p *RotateWriter) getMaxSize() int64 {
	if p.fileSize <= 0 {
		return int64(DefaultMaxSize * MegaByte)
	}

	return int64(p.fileSize) * int64(MegaByte)
}

// getFileDir returns the directory for the file path.
func (p *RotateWriter) getFileDir() string {
	return filepath.Dir(p.filePath)
}

// getPrefixExt returns the getCurrentFilePath part and extension part from the RotateWriter's
// getCurrentFilePath.
func (p *RotateWriter) getPrefixExt() (prefix, ext string) {
	filename := filepath.Base(p.filePath)

	ext = filepath.Ext(filename)
	prefix = filename[:len(filename)-len(ext)] + "."
	return prefix, ext
}

// compressLogFile compresses the given log file, removing the
// uncompressed log file if successful.
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

	var filenames []string
	fileDir := filepath.Dir(filePath)

	filepath.Walk(fileDir, func(path string, info os.FileInfo, err error) error {
		if info != nil && strings.Contains(info.Name(), rotatePrefix) {
			filenames = append(filenames, info.Name())
		}

		return nil
	})

	var maxCursor int64 = 0

	for _, filename := range filenames {
		subStr := filename[len(rotatePrefix)+1:]

		index := strings.Index(subStr, ".")
		if index != -1 {
			cursor, err := strconv.ParseInt(subStr[:index], 10, 32)
			if err == nil && cursor > maxCursor {
				maxCursor = cursor
			}
		}
	}

	return int32(maxCursor) + 1
}

// logFile is a convenience struct to return the path and its embedded modify time.
type logFile struct {
	modifyTime time.Time

	os.FileInfo
}

// logFiles sorts by newest time formatted in the name.
type logFiles []*logFile

func (f logFiles) Less(i, j int) bool {
	return f[i].modifyTime.After(f[j].modifyTime)
}

func (f logFiles) Swap(i, j int) {
	f[i], f[j] = f[j], f[i]
}

func (f logFiles) Len() int {
	return len(f)
}
