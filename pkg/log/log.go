package log

import (
	"context"

	"github.com/sirupsen/logrus"
)

const (
	logContextKey = "log"
)

var (
	DefaultLogger = New()
)

func New() *logrus.Logger {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.AddHook(logFieldsHook{})
	return log
}

type logFieldsHook struct {
}

func (logFieldsHook) Levels() []logrus.Level {
	return logrus.AllLevels
}
func (logFieldsHook) Fire(entry *logrus.Entry) error {
	entry.Data = mergeFields(getLogFieldsContext(entry.Context), entry.Data)
	return nil
}

func AddLogFieldsToContext(ctx context.Context, newFields logrus.Fields) context.Context {
	return context.WithValue(ctx, logContextKey, mergeFields(getLogFieldsContext(ctx), newFields))
}

func getLogFieldsContext(ctx context.Context) map[string]interface{} {
	if ctx == nil {
		return map[string]interface{}{}
	}
	fields, ok := ctx.Value(logContextKey).(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return fields
}

func mergeFields(fieldsList ...map[string]interface{}) map[string]interface{} {
	fields := map[string]interface{}{}
	for _, f := range fieldsList {
		for k, v := range f {
			fields[k] = v
		}
	}
	return fields
}
