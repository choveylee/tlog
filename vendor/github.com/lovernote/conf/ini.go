package conf

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var (
	defaultSection = "default" // default section

	numCommentStr = []byte{'#'} // number signal
	semCommentStr = []byte{';'} // semicolon signal
	emptyStr      = []byte{}
	equalStr      = []byte{'='} // equal signal
	quoteStr      = []byte{'"'} // quote signal
	secStartStr   = []byte{'['} // section start signal
	secEndStr     = []byte{']'} // section end signal
	lineBreakStr  = "\n"
)

type IniMgr struct {
}

// Parse creates a new Config and parses the file configuration from the named file.
func (this *IniMgr) Parse(name string) (*IniData, error) {
	return this.parseFile(name)
}

func (this *IniMgr) parseFile(name string) (*IniData, error) {
	data, err := ioutil.ReadFile(name)

	if err != nil {
		return nil, err
	}

	return this.parseData(filepath.Dir(name), data)
}

func (this *IniMgr) parseData(dir string, data []byte) (*IniData, error) {
	cfg := &IniData{
		data:       make(map[string]map[string]string),
		secComment: make(map[string]string),
		keyComment: make(map[string]string),

		RWMutex: sync.RWMutex{},
	}

	cfg.Lock()
	defer cfg.Unlock()

	var comment bytes.Buffer

	buf := bufio.NewReader(bytes.NewBuffer(data))

	// check the BOM
	head, err := buf.Peek(3)

	if err == nil && head[0] == 239 && head[1] == 187 && head[2] == 191 {
		for i := 1; i <= 3; i++ {
			buf.ReadByte()
		}
	}

	section := defaultSection

	for {
		line, _, err := buf.ReadLine()

		if err == io.EOF {
			break
		}

		//It might be a good idea to throw a error on all unknonw errors?
		if _, ok := err.(*os.PathError); ok {
			return nil, err
		}

		line = bytes.TrimSpace(line)

		if bytes.Equal(line, emptyStr) {
			continue
		}

		var bComment []byte

		switch {
		case bytes.HasPrefix(line, numCommentStr):
			bComment = numCommentStr
		case bytes.HasPrefix(line, semCommentStr):
			bComment = semCommentStr
		}

		if bComment != nil {
			line = bytes.TrimLeft(line, string(bComment))

			// Need append to a new line if multi-line comments.
			if comment.Len() > 0 {
				comment.WriteByte('\n')
			}

			comment.Write(line)

			continue
		}

		if bytes.HasPrefix(line, secStartStr) && bytes.HasSuffix(line, secEndStr) {
			section = strings.ToLower(string(line[1 : len(line)-1])) // section name case insensitive

			if comment.Len() > 0 {
				cfg.secComment[section] = comment.String()
				comment.Reset()
			}

			if _, ok := cfg.data[section]; !ok {
				cfg.data[section] = make(map[string]string)
			}

			continue
		}

		if _, ok := cfg.data[section]; !ok {
			cfg.data[section] = make(map[string]string)
		}

		keyValue := bytes.SplitN(line, equalStr, 2)

		key := string(bytes.TrimSpace(keyValue[0])) // key name case insensitive
		key = strings.ToLower(key)

		// handle include "other.conf"
		if len(keyValue) == 1 && strings.HasPrefix(key, "include") {
			includefiles := strings.Fields(key)

			if includefiles[0] == "include" && len(includefiles) == 2 {
				otherfile := strings.Trim(includefiles[1], "\"")

				if !filepath.IsAbs(otherfile) {
					otherfile = filepath.Join(dir, otherfile)
				}

				i, err := this.parseFile(otherfile)

				if err != nil {
					return nil, err
				}

				for sec, dt := range i.data {
					if _, ok := cfg.data[sec]; !ok {
						cfg.data[sec] = make(map[string]string)
					}

					for k, v := range dt {
						cfg.data[sec][k] = v
					}
				}

				for sec, comm := range i.secComment {
					cfg.secComment[sec] = comm
				}

				for k, comm := range i.keyComment {
					cfg.keyComment[k] = comm
				}

				continue
			}
		}

		if len(keyValue) != 2 {
			return nil, errors.New("read the content error: \"" + string(line) + "\", should key = val")
		}

		val := bytes.TrimSpace(keyValue[1])

		if bytes.HasPrefix(val, quoteStr) {
			val = bytes.Trim(val, `"`)
		}

		cfg.data[section][key] = string(val)

		if comment.Len() > 0 {
			cfg.keyComment[section+"."+key] = comment.String()
			comment.Reset()
		}
	}

	return cfg, nil
}

// IniData A Config represents the ini configuration.
// When set and get value, support key as section:name type.
type IniData struct {
	data       map[string]map[string]string // section=> key:val
	secComment map[string]string            // section : comment
	keyComment map[string]string            // id: []{comment, key...}; id 1 is for main comment.

	sync.RWMutex
}

func (this *IniData) GetData1() map[string]map[string]string {
	return this.data
}

func (this *IniData) GetData2() map[string]string {
	return this.secComment
}

func (this *IniData) GetData3() map[string]string {
	return this.keyComment
}

// Bool returns the boolean value for a given key.
func (this *IniData) Bool(key string) (bool, error) {
	return parseBool(this.getdata(key))
}

// DefaultBool returns the boolean value for a given key.
// if err != nil return defaltval
func (this *IniData) DefaultBool(key string, defaultval bool) bool {
	v, err := this.Bool(key)

	if err != nil {
		return defaultval
	}

	return v
}

// Int returns the integer value for a given key.
func (this *IniData) Int(key string) (int, error) {
	return strconv.Atoi(this.getdata(key))
}

// DefaultInt returns the integer value for a given key.
// if err != nil return defaltval
func (this *IniData) DefaultInt(key string, defaultval int) int {
	v, err := this.Int(key)

	if err != nil {
		return defaultval
	}

	return v
}

// Int64 returns the int64 value for a given key.
func (this *IniData) Int64(key string) (int64, error) {
	return strconv.ParseInt(this.getdata(key), 10, 64)
}

// DefaultInt64 returns the int64 value for a given key.
// if err != nil return defaltval
func (this *IniData) DefaultInt64(key string, defaultval int64) int64 {
	v, err := this.Int64(key)

	if err != nil {
		return defaultval
	}

	return v
}

// Float returns the float value for a given key.
func (this *IniData) Float(key string) (float64, error) {
	return strconv.ParseFloat(this.getdata(key), 64)
}

// DefaultFloat returns the float64 value for a given key.
// if err != nil return defaltval
func (this *IniData) DefaultFloat(key string, defaultval float64) float64 {
	v, err := this.Float(key)

	if err != nil {
		return defaultval
	}

	return v
}

// String returns the string value for a given key.
func (this *IniData) String(key string) string {
	return this.getdata(key)
}

// DefaultString returns the string value for a given key.
// if err != nil return defaltval
func (this *IniData) DefaultString(key string, defaultval string) string {
	v := this.String(key)

	if v == "" {
		return defaultval
	}

	return v
}

// Strings returns the []string value for a given key.
// Return nil if config value does not exist or is empty.
func (this *IniData) Strings(key string) []string {
	v := this.String(key)

	if v == "" {
		return nil
	}

	return strings.Split(v, ";")
}

// DefaultStrings returns the []string value for a given key.
// if err != nil return defaltval
func (this *IniData) DefaultStrings(key string, defaultval []string) []string {
	v := this.Strings(key)

	if v == nil {
		return defaultval
	}

	return v
}

// GetSection returns map for the given section
func (this *IniData) GetSection(section string) (map[string]string, error) {
	if v, ok := this.data[section]; ok {
		return v, nil
	}

	return nil, errors.New("not exist section")
}

// section.key or key
func (this *IniData) getdata(key string) string {
	if len(key) == 0 {
		return ""
	}

	this.RLock()
	defer this.RUnlock()

	var (
		section, k string
		sectionKey = strings.Split(strings.ToLower(key), "::")
	)

	if len(sectionKey) >= 2 {
		section = sectionKey[0]
		k = sectionKey[1]
	} else {
		section = defaultSection
		k = sectionKey[0]
	}

	if v, ok := this.data[section]; ok {
		if vv, ok := v[k]; ok {
			return vv
		}
	}

	return ""
}

func parseBool(val interface{}) (value bool, err error) {
	if val != nil {
		switch v := val.(type) {
		case bool:
			return v, nil
		case string:
			switch v {
			case "1", "t", "T", "true", "TRUE", "True", "YES", "yes", "Yes", "Y", "y", "ON", "on", "On":
				return true, nil
			case "0", "f", "F", "false", "FALSE", "False", "NO", "no", "No", "N", "n", "OFF", "off", "Off":
				return false, nil
			}
		case int8, int32, int64:
			strV := fmt.Sprintf("%d", v)
			if strV == "1" {
				return true, nil
			} else if strV == "0" {
				return false, nil
			}
		case float64:
			if v == 1.0 {
				return true, nil
			} else if v == 0.0 {
				return false, nil
			}
		}
		return false, fmt.Errorf("parsing %q: invalid syntax", val)
	}
	return false, fmt.Errorf("parsing <nil>: invalid syntax")
}
