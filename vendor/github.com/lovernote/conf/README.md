## 基于ini的配置文件读取库

	import "github.com/lovernote/conf"

### a. 配置文件默认路径为程序同级的conf.ini

### b. 配置文件格式如下
	#conf.ini#

	app_name = "ads-svc"
	
	rpc_port = ":9012"
	
	run_mode = "dev"
	
	consul_addr = "127.0.0.1"
	consul_port = 8500
	
	health_addr = "10.234.200.128"
	health_port = 9013
	
	log_path = "/data/qingting/logs/ads-svc.log"
	
	sentry_dsn = ""
	
	[dev]
	ad_server_db = ""
	
	[prod]
	ad_server_db = "root:root@(127.0.0.1:3306)/adserver?timeout=30s&parseTime=true&loc=Local&charset=utf8mb4"

### c. 配置文件读取如下

	adServerUrl := ""
	
	runMode := conf.GetIniData().String("run_mode")

	if runMode == "prod" {
		adServerUrl = conf.GetIniData().String("prod::ad_server_db")
	} else {
		adServerUrl = conf.GetIniData().String("dev::ad_server_db")
	}

### d. 具体支持的类型, 参见ini.go文件