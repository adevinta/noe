package log

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

type logrusLogger struct {
	logger *logrus.Entry
	level  logrus.Level
}

var _ logr.LogSink = &logrusLogger{}

func ContextualizeLogr(logger logr.Logger, ctx context.Context) logr.Logger {
	values := []interface{}{}
	for k, v := range getLogFieldsContext(ctx) {
		values = append(values, k, v)
	}
	return logger.WithValues(values...)
}

// NewLogr returns a logrus logger compliant with the logr interface.
func NewLogr(logger *logrus.Logger) logr.Logger {
	return logr.New(&logrusLogger{
		logger: logrus.NewEntry(logger),
		level:  logrus.InfoLevel,
	})
}

// Error logs an error, with the given message and key/value pairs as context.
// It functions similarly to calling Info with the "error" named value, but may
// have unique behavior, and should be preferred for logging errors (see the
// package documentations for more information).
//
// The msg field should be used to add context to any underlying error,
// while the err field should be used to attach the actual error that
// triggered this log line, if present.
func (l *logrusLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	l.Info(0, msg, append(keysAndValues, "error", err)...)
}

// WithValues adds some key-value pairs of context to a logger.
// See Info for documentation on how key/value pairs work.
func (l *logrusLogger) WithValues(keysAndValues ...interface{}) logr.LogSink {
	return &logrusLogger{
		logger: l.logrusWithValues(keysAndValues...),
		level:  l.level,
	}
}

func (l *logrusLogger) Init(logr.RuntimeInfo) {
}

// WithName adds a new element to the logger's name.
// Successive calls with WithName continue to append
// suffixes to the logger's name.  It's strongly reccomended
// that name segments contain only letters, digits, and hyphens
// (see the package documentation for more information).
func (l *logrusLogger) WithName(name string) logr.LogSink {
	n, ok := l.logger.Data["name"]
	if !ok {
		return l.WithValues("name", name)
	}
	return l.WithValues("name", fmt.Sprintf("%v.%s", n, name))
}

// Info logs a non-error message with the given key/value pairs as context.
//
// The msg argument should be used to add some constant description to
// the log line.  The key/value pairs can then be used to add additional
// variable information.  The key/value pairs should alternate string
// keys and arbitrary values.
func (l *logrusLogger) Info(level int, msg string, keysAndValues ...interface{}) {
	if l.Enabled(level) {
		l.logrusWithValues(keysAndValues...).Log(logrLevelToLogrus(level), msg)
	}
}

// Enabled tests whether this InfoLogger is enabled.  For example,
// commandline flags might be used to set the logging verbosity and disable
// some info logs.
func (l *logrusLogger) Enabled(level int) bool {
	if l.logger == nil || l.logger.Logger == nil {
		return false
	}
	if logrLevelToLogrus(level) <= l.logger.Logger.Level {
		return true
	}
	return false
}

// WithValues adds some key-value pairs of context to a logger.
// See Info for documentation on how key/value pairs work.
func (l *logrusLogger) logrusWithValues(keysAndValues ...interface{}) *logrus.Entry {
	fields := logrus.Fields{}
	key := ""
	for _, field := range keysAndValues {
		var ok bool
		var k string
		if key == "" {
			k, ok = field.(string)
			if ok {
				key = k
			} else {
				key = fmt.Sprintf("%v", field)
			}
		} else {
			fields[key] = field
			key = ""
		}
	}
	return l.logger.WithFields(fields)
}

func logrLevelToLogrus(level int) logrus.Level {
	switch level {
	case 0:
		return logrus.ErrorLevel
	case 1:
		return logrus.WarnLevel
	case 2:
		return logrus.InfoLevel
	case 3:
		return logrus.DebugLevel
	case 4:
		return logrus.TraceLevel
	default:
		return logrus.TraceLevel
	}
}
