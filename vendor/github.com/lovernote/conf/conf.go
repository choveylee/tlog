package conf

import (
	"os"
	"path/filepath"
)

var (
	_ini_data *IniData
)

func init() {
	appPath, err := filepath.Abs(filepath.Dir(os.Args[0]))

	if err != nil {
		panic(err)
	}

	workPath, err := os.Getwd()

	if err != nil {
		panic(err)
	}

	// conf.ini存在判断
	confPath := filepath.Join(workPath, "conf.ini")

	_, err = os.Stat(confPath)

	if err != nil && os.IsNotExist(err) == true {
		confPath = filepath.Join(appPath, "conf.ini")

		_, err = os.Stat(confPath)

		if err != nil && os.IsNotExist(err) == true {
			panic(err)
		}
	}

	iniMgr := &IniMgr{}

	_ini_data, err = iniMgr.Parse(confPath)

	if err != nil {
		panic(err)
	}
}

func GetIniData() *IniData {
	return _ini_data
}
