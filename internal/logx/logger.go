package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Level = logrus.Level

const (
	LevelDebug = logrus.DebugLevel
	LevelInfo  = logrus.InfoLevel
	LevelWarn  = logrus.WarnLevel
	LevelError = logrus.ErrorLevel
)

type Config struct {
	Level   Level
	Pretty  bool
	Console bool
	File    *File

	StreamBuffer int
}

type File struct {
	Path       string
	MaxSize    int
	MaxBackups int
	MaxAge     int
	Compress   bool
}

type Logger struct {
	logger *logrus.Logger
	stream chan string
}

var (
	defaultMu sync.RWMutex
	defaultL  = newNoop()
)

func newNoop() *Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.PanicLevel)
	return &Logger{logger: l, stream: make(chan string)}
}

func New(cfg Config) (*Logger, error) {
	l := logrus.New()

	outputs := []io.Writer{}
	if cfg.Console || cfg.File == nil || cfg.File.Path == "" {
		outputs = append(outputs, os.Stdout)
	}
	if cfg.File != nil && cfg.File.Path != "" {
		rotator := &lumberjack.Logger{
			Filename:   cfg.File.Path,
			MaxSize:    cfg.File.MaxSize,
			MaxBackups: cfg.File.MaxBackups,
			MaxAge:     cfg.File.MaxAge,
			Compress:   cfg.File.Compress,
		}
		outputs = append(outputs, rotator)
	}
	if len(outputs) == 0 {
		outputs = append(outputs, io.Discard)
	}
	l.SetOutput(io.MultiWriter(outputs...))
	l.SetLevel(cfg.Level)
	l.SetFormatter(&logrus.TextFormatter{
		ForceColors:     cfg.Pretty,
		DisableColors:   !cfg.Pretty,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	buf := cfg.StreamBuffer
	if buf <= 0 {
		buf = 128
	}
	return &Logger{logger: l, stream: make(chan string, buf)}, nil
}

func SetDefault(l *Logger) {
	if l == nil {
		return
	}
	defaultMu.Lock()
	defaultL = l
	defaultMu.Unlock()
}

func defaultLogger() *Logger {
	defaultMu.RLock()
	l := defaultL
	defaultMu.RUnlock()
	return l
}

func (l *Logger) Stream() <-chan string {
	if l == nil {
		return nil
	}
	return l.stream
}

func (l *Logger) emit(level, msg string) {
	if l == nil {
		return
	}
	line := fmt.Sprintf("%s [%s] %s", time.Now().Format("15:04:05"), level, msg)
	select {
	case l.stream <- line:
	default:
	}
}

func (l *Logger) Debugf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.logger.Debug(msg)
	l.emit("DBG", msg)
}

func (l *Logger) Infof(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.logger.Info(msg)
	l.emit("INF", msg)
}

func (l *Logger) Warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.logger.Warn(msg)
	l.emit("WRN", msg)
}

func (l *Logger) Errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.logger.Error(msg)
	l.emit("ERR", msg)
}

func Debugf(format string, args ...any) { defaultLogger().Debugf(format, args...) }
func Infof(format string, args ...any)  { defaultLogger().Infof(format, args...) }
func Warnf(format string, args ...any)  { defaultLogger().Warnf(format, args...) }
func Errorf(format string, args ...any) { defaultLogger().Errorf(format, args...) }
