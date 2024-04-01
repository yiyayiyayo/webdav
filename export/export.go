package webdav

import (
	"errors"
	"github.com/hacdias/webdav/v4/cmd"
	v "github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"net"
	"net/http"
	"strings"
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
		callback.OnMessage(-1, "Already running.")
		return
	}

	config := cmd.InitConfig(configFile)

	// init log
	loggerConfig := zap.NewProductionConfig()
	loggerConfig.DisableCaller = true
	if config.Debug {
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	loggerConfig.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	loggerConfig.Encoding = config.LogFormat
	logger, err := loggerConfig.Build(zap.Hooks(func(entry zapcore.Entry) error {
		callback.OnMessage(int(entry.Level), entry.Message)
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
		callback.OnMessage(-1, err.Error())
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
			ins.callback.OnMessage(-1, "Error with "+err.Error())
			instance = nil
		}
	}(instance, tls, cert, key)

	callback.OnStart(listener.Addr().String())
}

func Stop() {
	if ins := instance; ins != nil {
		if err := ins.server.Close(); err != nil {
			ins.callback.OnMessage(-1, err.Error())
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
