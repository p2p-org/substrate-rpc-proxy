package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	"github.com/sirupsen/logrus"
)

type LoggerOption func(context.Context) context.Context

func LogWithParams() LoggerOption {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, LogWithParamsCtxKey, true)
	}
}

var log *logrus.Logger

func LogWithIgnoreMethods(methods []string) LoggerOption {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, LogWithIgnoreMethodsCtxKey, methods)
	}
}

func Logger(l *logrus.Logger, opts ...LoggerOption) func(http.Handler) http.Handler {
	log = l
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, LogLoggerCtxKey, log)
			for _, opt := range opts {
				ctx = opt(ctx)
			}
			//ctx = context.WithValue(ctx, LogWithParamsCtxKey, withParams)
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

func LogSubscriptionDetails(ctx context.Context, client string, upstream string, f dto.RPCFrame) {

	logFields := make(logrus.Fields)
	logFields["client"] = client
	logFields["upsteam"] = upstream
	logFields["direction"] = "server-push"
	logFields["sub_id"] = dto.MustMap(f.Params).MustString("subscription")
	logFields["method"] = f.Method
	chictx := chi.RouteContext(ctx)

	log.WithFields(logFields).Debugf("%s %s", GetConnectionID(ctx), chictx.RoutePath)
}

func LogRequestDetails(r *http.Request) {
	ctx := r.Context()

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
		logFields["id"] = 0
	}
	if ignoreMethods := ctx.Value(LogWithIgnoreMethodsCtxKey); ignoreMethods != nil {
		m, ok := ignoreMethods.([]string)
		if ok {
			for _, method := range m {
				if strings.EqualFold(method, logFields["method"].(string)) {
					return
				}
			}
		}
	}

	if startTime := ctx.Value(LogRequestStartTime); startTime != nil {
		logFields["duration"] = fmt.Sprintf("%fs", time.Since(startTime.(time.Time)).Seconds())
	}
	logFields["direction"] = "request"

	chictx := chi.RouteContext(ctx)

	log.WithFields(logFields).Debugf("%s %s", GetConnectionID(ctx), chictx.RoutePath)
}
