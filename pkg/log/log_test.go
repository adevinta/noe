package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adevinta/noe/pkg/log"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestContextLogger(t *testing.T) {
	ctx := context.Background()
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"key": "value"})

	logger := log.New()
	b := bytes.Buffer{}
	logger.Out = &b
	logger.WithContext(ctx).WithTime(time.Date(2021, 01, 01, 10, 20, 10, 0, time.UTC)).Info("hello world")
	loggedData := map[string]interface{}{}
	json.NewDecoder(&b).Decode(&loggedData)
	assert.Equal(
		t,
		map[string]interface{}{
			"key":   "value",
			"msg":   "hello world",
			"level": "info",
			"time":  "2021-01-01T10:20:10Z",
		},
		loggedData,
	)
}

func TestContextLoggerShouldFavourEntryFields(t *testing.T) {
	ctx := context.Background()
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"key": "from-context"})

	logger := log.New()
	b := bytes.Buffer{}
	logger.Out = &b
	logger.WithContext(ctx).WithField("key", "from-entry").Info("hello world")
	loggedData := map[string]interface{}{}
	json.NewDecoder(&b).Decode(&loggedData)
	assert.Equal(t, "from-entry", loggedData["key"])
}
