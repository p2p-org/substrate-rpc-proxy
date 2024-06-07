package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

func Logger(log *logrus.Logger, withParams bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, LogLoggerCtxKey, log)
			ctx = context.WithValue(ctx, LogWithParamsCtxKey, withParams)
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			LogRequestDetails(r)
		})
	}
}

func AddLogField(ctx context.Context, k string, v string) {
	entryFields := ctx.Value(LogFieldsCtxKey)
	if entryFields == nil {
		return
	}

	entryFields.(logrus.Fields)[k] = v
}

func GetLogField(ctx context.Context, k string) string {
	entryFields := ctx.Value(LogFieldsCtxKey)
	if entryFields == nil {
		return ""
	}
	field, ok := entryFields.(logrus.Fields)[k]
	if ok {
		return field.(string)
	}
	return ""
}

func GetLogger(ctx context.Context) *logrus.Logger {
	if log := ctx.Value(LogLoggerCtxKey); log != nil {
		return log.(*logrus.Logger)
	}
	return nil
}

func logFieldsFromRequest(r *http.Request) logrus.Fields {
	logFields := make(logrus.Fields)
	ctx := r.Context()
	lf := ctx.Value(LogFieldsCtxKey)
	if lf != nil {
		for k, v := range lf.(logrus.Fields) {
			logFields[k] = v
		}
	}

	if r.Header.Get("x-forwarded-host") != "" {
		logFields["forwarded_host"] = r.Header.Get("x-forwarded-host")
	}
	logFields["client"] = r.RemoteAddr
	return logFields
}

func LogSubscriptionDetails(r *http.Request, method string, subscriptionId string) {
	ctx := r.Context()
	log := GetLogger(ctx)
	if log == nil {
		return
	}
	logFields := logFieldsFromRequest(r)
	logFields["direction"] = "server-push"
	logFields["sub_id"] = subscriptionId
	logFields["method"] = method

	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	log.WithFields(logFields).Debugf("%s %s", GetConnectionID(ctx), path)
}

func LogRequestDetails(r *http.Request) {
	ctx := r.Context()
	log := GetLogger(ctx)
	if log == nil {
		return
	}

	logFields := logFieldsFromRequest(r)

	if withParams := ctx.Value(LogWithParamsCtxKey); withParams != nil && withParams.(bool) {
		if f := GetJsonRPCFrame(ctx); f != nil {
			logFields["method"] = f.Method
			logFields["id"] = f.Id
			logFields["params"] = fmt.Sprintf("%v", f.Params)
		}
	} else {
		logFields["method"] = GetRPCMethod(ctx)
		logFields["id"] = GetRPCRequestID(ctx)
	}
	if logFields["method"] == "" {
		logFields["method"] = "batch"
		logFields["id"] = GetConnectionID(ctx)
	}
	sec := -1.0
	if startTime := ctx.Value(LogRequestStartTime); startTime != nil {
		sec = time.Since(startTime.(time.Time)).Seconds()
	}
	logFields["duration"] = fmt.Sprintf("%fs", sec)
	logFields["direction"] = "request"

	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	log.WithFields(logFields).Debugf("%s %s", GetConnectionID(ctx), path)
}
