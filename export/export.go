package webdav

import (
	"encoding/json"
	"errors"
	"github.com/hacdias/webdav/v4/cmd"
	v "github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const (
	CodeStartFailed         = -0x1
	CodeStopFailed          = -0x2
	CodeStartAlreadyRunning = 0x01
	CodeMessage             = 0x10
	CodeRequest             = 0x20
)

var (
	instance *_Instance
)

type _Instance struct {
	listener net.Listener
	server   *http.Server
	callback Callback
}

func Start(configFile string, callback Callback) {
	if instance != nil {
		callback.OnMessage(CodeStartAlreadyRunning, "Already running.")
		return
	}

	httpLogger := func(request *http.Request, err error) {
		jsonString, _ := json.Marshal(map[string]interface{}{
			"method":         request.Method,
			"path":           request.URL.Path,
			"content_length": request.ContentLength,
			"close":          request.Close,
			"x_expected_entity_length": func() int64 {
				num, _ := strconv.ParseInt(request.Header.Get("X-Expected-Entity-Length"), 10, 64)
				return num
			}(),
			"error": func() string {
				if err == nil {
					return ""
				}
				return err.Error()
			}(),
		})
		callback.OnMessage(CodeRequest, string(jsonString))
	}

	config := cmd.InitConfig(configFile)
	config.Handler.Logger = httpLogger
	for _, u := range config.Users {
		u.Handler.Logger = httpLogger
	}

	// init log
	loggerConfig := zap.NewProductionConfig()
	loggerConfig.DisableCaller = true
	if config.Debug {
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	loggerConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	loggerConfig.Encoding = config.LogFormat
	logger, err := loggerConfig.Build(zap.Hooks(func(entry zapcore.Entry) error {
		callback.OnMessage(CodeMessage, entry.Message)
		return nil
	}))
	if err != nil {
		// if we fail to configure proper logging, then the user has deliberately
		// misconfigured the logger. Abort.
		panic(err)
	}
	zap.ReplaceGlobals(logger)
	defer func() {
		_ = zap.L().Sync()
	}()

	// Build address and listener
	laddr := getOpt("address", "0.0.0.0")
	var lnet string
	if strings.HasPrefix(laddr, "unix:") {
		laddr = laddr[5:]
		lnet = "unix"
	} else {
		laddr = laddr + ":" + getOpt("port", "0")
		lnet = "tcp"
	}
	listener, err := net.Listen(lnet, laddr)
	if err != nil {
		callback.OnMessage(CodeStartFailed, err.Error())
		return
	}

	instance = &_Instance{
		listener: listener,
		server:   &http.Server{Handler: config},
		callback: callback,
	}

	// Starts the server.
	tls := getOptB("tls", false)
	cert := getOpt("cert", "cert.pem")
	key := getOpt("key", "key.pem")
	go func(ins *_Instance, tls bool, cert string, key string) {
		var err error
		if tls {
			err = ins.server.ServeTLS(listener, cert, key)
		} else {
			err = ins.server.Serve(listener)
		}

		if errors.Is(err, http.ErrServerClosed) {
			ins.callback.OnStop()
		} else if err != nil {
			ins.callback.OnMessage(CodeStartFailed, err.Error())
		}
		instance = nil
	}(instance, tls, cert, key)

	if addr, ok := listener.Addr().(*net.TCPAddr); ok {
		callback.OnStart(strconv.Itoa(addr.Port))
	} else {
		callback.OnStart(listener.Addr().String())
	}
}

func Stop() {
	if ins := instance; ins != nil {
		if err := ins.server.Close(); err != nil {
			ins.callback.OnMessage(CodeStopFailed, err.Error())
		}
	}
}

type Callback interface {
	OnStart(port string)
	OnStop()
	OnMessage(code int, message string)
}

func getOpt(key string, defValue string) string {
	// If set through viper (env, config), return it.
	if v.IsSet(key) {
		return v.GetString(key)
	}

	// Otherwise use default value on flags.
	return defValue
}

func getOptB(key string, defValue bool) bool {
	// If set through viper (env, config), return it.
	if v.IsSet(key) {
		return v.GetBool(key)
	}

	// Otherwise use default value on flags.
	return defValue
}
