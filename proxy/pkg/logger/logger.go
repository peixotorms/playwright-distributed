package logger

import (
	"os"

	"proxy/pkg/config"

	"github.com/sirupsen/logrus"
)

var Log *logrus.Logger

func Init(cfg *config.Config) {
	Log = logrus.New()

	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	Log.SetLevel(level)

	if cfg.LogFormat == "json" {
		Log.SetFormatter(&logrus.JSONFormatter{})
	} else {
		Log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
	}

	Log.SetOutput(os.Stdout)
}

func Debug(format string, args ...interface{}) {
	Log.Debugf(format, args...)
}

func Info(format string, args ...interface{}) {
	Log.Infof(format, args...)
}

func Warn(format string, args ...interface{}) {
	Log.Warnf(format, args...)
}

func Error(format string, args ...interface{}) {
	Log.Errorf(format, args...)
}

func Fatal(format string, args ...interface{}) {
	Log.Fatalf(format, args...)
}

func WithField(key string, value interface{}) *logrus.Entry {
	return Log.WithField(key, value)
}

func WithFields(fields logrus.Fields) *logrus.Entry {
	return Log.WithFields(fields)
}
