package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adevinta/noe/pkg/log"
	gologr "github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type timeSetterHook struct {
}

func (t timeSetterHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (t timeSetterHook) Fire(entry *logrus.Entry) error {
	entry.Time = time.Date(2020, 03, 13, 14, 00, 0, 0, time.UTC)
	return nil
}

func initTestLogrus() (gologr.Logger, *bytes.Buffer) {
	b := new(bytes.Buffer)
	l := logrus.New()
	l.AddHook(timeSetterHook{})
	l.SetLevel(logrus.TraceLevel)
	l.SetFormatter(&logrus.JSONFormatter{})
	l.SetOutput(b)
	return log.NewLogr(l), b
}

func TestError(t *testing.T) {
	tested, b := initTestLogrus()
	tested.Error(errors.New("testError"), "this is a test", "some-context", "help")
	assert.JSONEq(t, `{"error":"testError","some-context": "help","level":"error","msg":"this is a test","time":"2020-03-13T14:00:00Z"}`, b.String())
}

func TestWithName(t *testing.T) {
	tested, b := initTestLogrus()
	tested.V(2).WithName("pkg").WithName("method").Info("hello world")
	assert.JSONEq(t, `{"level":"info","msg":"hello world", "name": "pkg.method","time":"2020-03-13T14:00:00Z"}`, b.String())
}

func TestWithLevel(t *testing.T) {
	tested, b := initTestLogrus()
	tested.V(0).Info("hello world")
	assert.JSONEq(t, `{"level":"error","msg":"hello world","time":"2020-03-13T14:00:00Z"}`, b.String())

	b.Reset()
	tested.V(1).Info("hello world")
	assert.JSONEq(t, `{"level":"warning","msg":"hello world","time":"2020-03-13T14:00:00Z"}`, b.String())

	b.Reset()
	tested.V(2).Info("hello world")
	assert.JSONEq(t, `{"level":"info","msg":"hello world","time":"2020-03-13T14:00:00Z"}`, b.String())

	b.Reset()
	tested.V(3).Info("hello world")
	assert.JSONEq(t, `{"level":"debug","msg":"hello world","time":"2020-03-13T14:00:00Z"}`, b.String())

	b.Reset()
	tested.V(4).Info("hello world")
	assert.JSONEq(t, `{"level":"trace","msg":"hello world","time":"2020-03-13T14:00:00Z"}`, b.String())

	b.Reset()
	tested.V(5).Info("hello world")
	assert.JSONEq(t, `{"level":"trace","msg":"hello world","time":"2020-03-13T14:00:00Z"}`, b.String())
}

func TestEnabled(t *testing.T) {
	tested, _ := initTestLogrus()
	assert.True(t, tested.Enabled())

	tested = log.NewLogr(nil)
	assert.False(t, tested.Enabled())
}

func TestNilDoesNotCrash(t *testing.T) {
	tested := log.NewLogr(nil)

	tested.Info("should not crash")
	tested.Error(errors.New("err"), "should not crash")
	tested.WithName("some")
	tested.WithValues("key", "value")
	tested.V(9).Info("should not crash")
}

func TestContextualizeLogr(t *testing.T) {
	ctx := context.Background()
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"key": "value"})

	l := log.New()
	b := bytes.Buffer{}
	l.Out = &b

	logger := log.NewLogr(l)

	log.ContextualizeLogr(logger, ctx).Info("hello world")
	loggedData := map[string]interface{}{}
	json.NewDecoder(&b).Decode(&loggedData)

	assert.Equal(t, "value", loggedData["key"])
}
