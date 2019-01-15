package tlog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	_file *os.File

	_debug *log.Logger
	_info  *log.Logger
	_warn  *log.Logger
	_err   *log.Logger

	_level int

	_rotate *rotate

	sync.RWMutex
}

type rotate struct {
	_size int64

	_expired  time.Duration
	_interval time.Duration
}

func NewLogger(path string, level int) *Logger {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)

	if err != nil {
		_std_error.Fatalf("new logger (%s) err (%v).", path, err)
	}

	logger := &Logger{
		_file: file,

		_level: level,

		_rotate: &rotate{
			_size:     GB,
			_expired:  time.Hour * 24 * 7,
			_interval: time.Hour,
		},
	}

	logger.setLogLevel(file, level)

	go logger.monitorLog()

	return logger
}

// Func - 设置Logger级别
func (this *Logger) setLogLevel(file *os.File, level int) {
	switch {
	case level >= LOG_LEVEL_DEBUG:
		this._debug = log.New(file, "\033[0;36mDEBUG:\033[0m ", log.LstdFlags|log.Lshortfile)
		fallthrough
	case level >= LOG_LEVEL_INFO:
		this._info = log.New(file, "INFO : ", log.LstdFlags|log.Lshortfile)
		fallthrough
	case level >= LOG_LEVEL_WARN:
		this._warn = log.New(file, "\033[0;35mWARN :\033[0m ", log.LstdFlags|log.Lshortfile)
		fallthrough
	case level >= LOG_LEVEL_ERROR:
		this._err = log.New(file, "\033[0;31mERROR:\033[0m ", log.LstdFlags|log.Lshortfile)
	}

	switch {
	case level < LOG_LEVEL_ERROR:
		this._err = nil
		fallthrough
	case level < LOG_LEVEL_WARN:
		this._warn = nil
		fallthrough
	case level < LOG_LEVEL_INFO:
		this._info = nil
		fallthrough
	case level < LOG_LEVEL_DEBUG:
		this._debug = nil
	}
}

// Func - 获取TLog文件大小
func (this *Logger) getLogSize() int64 {
	this.RLock()
	defer this.RUnlock()

	fi, err := this._file.Stat()

	if err != nil {
		_std_warn.Printf("get log size err (stat %v).\n", err)

		return 0
	}

	return fi.Size()
}

// Func - 截断并重命名超过Interval的Log文件
func (this *Logger) truncLog(filepath, ext string) {
	this.Lock()
	defer this.Unlock()

	err := this._file.Close()

	if err != nil {
		_std_warn.Printf("trunc log err (close %v).\n", err)

		return
	}

	err = os.Rename(filepath, filepath+ext)

	if err != nil {
		_std_warn.Printf("trunc log err (rename %v).\n", err)
	}

	file, err := os.OpenFile(filepath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)

	if err != nil {
		_std_warn.Printf("trunc log err (open %v).\n", err)

		return
	}

	// 重置文件写入
	this.setLogLevel(file, this._level)

	this._file = file
}

// Func - 生成Logg文件后缀
func suffix(t time.Time) string {
	year, month, day := t.Date()

	return "-" + fmt.Sprintf("%04d%02d%02d%02d", year, month, day, t.Hour())
}

// Func - 截断获得下一次指定时间段的时间
func toNextBound(duration time.Duration) time.Duration {
	return time.Now().Truncate(duration).Add(duration).Sub(time.Now())
}

// Func - Log监听处理函数
func (this *Logger) monitorLog() error {
	interval := time.After(toNextBound(this._rotate._interval))
	expired := time.After(LOG_CHECK_EXPIRED)

	// 按照文件大小分割文件后缀
	sizeExt := 1

	fn := filepath.Base(this._file.Name())

	fp, err := filepath.Abs(this._file.Name())

	if err != nil {
		_std_error.Fatalf("monitor log err (%v).", err)
	}

	for {
		var size <-chan time.Time
		if toNextBound(this._rotate._interval) != LOG_CHECK_INTERVAL {
			size = time.After(LOG_CHECK_INTERVAL)
		}
		select {
		case t := <-interval:
			// 自定义生成新的Logger文件
			interval = time.After(this._rotate._interval)
			this.truncLog(fp, suffix(t))
			sizeExt = 1

			_std_info.Printf("monitor log info (truncated by interval).\n")
		case <-expired:
			// 删除过期的Logger文件
			expired = time.After(LOG_CHECK_EXPIRED)

			err := filepath.Walk(filepath.Dir(fp),
				func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil
					}

					isLog := strings.Contains(info.Name(), fn)

					if time.Since(info.ModTime()) > this._rotate._expired && isLog && info.IsDir() == false {
						if err := os.Remove(path); err != nil {
							return err
						}

						_std_info.Printf("monitor log (%s) info (remove by expired).\n", path)
					}
					return nil
				})

			if err != nil {
				_std_error.Printf("monitor log err (remove %v).\n", err)
			}
		case t := <-size:
			// 文件大小超过上限
			if this.getLogSize() < this._rotate._size {
				break
			}

			this.truncLog(fp, suffix(t)+"."+strconv.Itoa(sizeExt))

			sizeExt++

			_std_info.Printf("monitor log info (trunc by size).\n")
		}
	}
}

// Func - 输出Debug日志
func (this *Logger) Debug(format string, v ...interface{}) {
	this.RLock()
	defer this.RUnlock()

	if this._debug != nil {
		this._debug.Output(3, fmt.Sprintln(fmt.Sprintf(format, v...)))
	}
}

// Func - 输出Info日志
func (this *Logger) Info(format string, v ...interface{}) {
	this.RLock()
	defer this.RUnlock()

	if this._info != nil {
		this._info.Output(3, fmt.Sprintln(fmt.Sprintf(format, v...)))
	}
}

// Func - 输出Warn日志
func (this *Logger) Warn(format string, v ...interface{}) {
	_std_warn.Output(3, fmt.Sprintln(fmt.Sprintf(format, v...)))

	this.RLock()
	defer this.RUnlock()

	if this._warn != nil {
		this._warn.Output(3, fmt.Sprintln(fmt.Sprintf(format, v...)))
	}
}

// Func - 输出Error日志
func (this *Logger) Error(format string, v ...interface{}) {
	_std_error.Output(3, fmt.Sprintln(fmt.Sprintf(format, v...)))

	this.RLock()
	defer this.RUnlock()

	if this._err != nil {
		this._err.Output(3, fmt.Sprintln(fmt.Sprintf(format, v...)))
	}
}
