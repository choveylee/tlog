## 日志库, 依赖conf库

	import "github.com/lovernote/conf"

### a. 配置文件需要配置如下字段
 	#conf.ini
	
	app_name = "ads-svc"
	
	run_mode = "dev"
	
	log_path = "/data/qingting/logs/ads-svc.log"
	
	sentry_dsn = ""


### b. 默认dev模式下, 错误日志不上报sentry, 如果sentry配置项为空, 也不上报

### c. 错误输出支持如下四个级别, prod模式, 不记录Debug日志
		tlog.Debug(format, ...)
		tlog.Info(format, ...)
		tlog.Warn(format, ...)
		tlog.Error(format, ...)

### d. 使用方法如下
	
    val, err := strconv.Atoi("a") 
	
	errMsg := tlog.Error("convert err (%v)", err)
	
	logErr := tlog.NewLogError(err, errMsg)    // 构造LogError
	
	errMsgEx := fmt.Printf("uppder err (%v).", logErr.Error())
	
	logErr.AttachErrMsg(errMsgEx)   // 为LogError添加额外错误信息
	
	logErr.AttachRequest(req)       // 为LogError添加Request请求信息(Sentry使用)
	
	tlog.AsyncSend(logErr)          // 上报错误信息到Sentry
	
### e. 本地日志存储, 默认每个小时或者超过指定大小会截断, 同时定期会清理过期的日志文件
