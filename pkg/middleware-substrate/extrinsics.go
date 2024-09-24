package substrate

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/decoder"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/rpc"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/storage"

	"github.com/sirupsen/logrus"
)

type ExtrinsicsInspector struct {
	db               *storage.DBClient
	mon              monitoring.Observer
	decoder          *decoder.Decoder
	client           *rpc.RPCClient
	log              *logrus.Logger
	methodsToInspect []string
	headerNextUpdate time.Time
	headerCurrent    dto.Mapped
}

func NewExtrinsicsInspector(l *logrus.Logger, db *storage.DBClient, dec *decoder.Decoder, client *rpc.RPCClient, methodsToInspect []string) *ExtrinsicsInspector {
	return &ExtrinsicsInspector{
		db:               db,
		decoder:          dec,
		log:              l,
		client:           client,
		methodsToInspect: methodsToInspect,
	}
}

func (i *ExtrinsicsInspector) GetMonitoringEventTypes() []string {
	return []string{
		monitoring.MetricExtrinsicsCount,
	}
}

func (i *ExtrinsicsInspector) SetObserver(mon monitoring.Observer) {
	i.mon = mon
}

func (i *ExtrinsicsInspector) LogToDB(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var err error
		frame := middleware.GetJsonRPCFrame(r.Context())
		if frame != nil {
			needToInspect := false
			for j := 0; j < len(i.methodsToInspect); j++ {
				if strings.EqualFold(i.methodsToInspect[j], frame.Method) {
					needToInspect = true
					break
				}
			}
			if needToInspect {
				if extr, _ := i.decoder.GetExtrinsicDataFrom(ctx, frame.Params); extr != nil {
					i.mon.ProcessEvent(monitoring.MetricExtrinsicsCount, 1)
					go func(ctx context.Context) {
						if time.Until(i.headerNextUpdate) < 0 {
							header, err := i.client.ChainGetHeader(ctx, "")
							if err == nil {
								i.log.WithField("headerNo", header.MustInt("number")).Info("obtained latest header")
								i.headerNextUpdate = time.Now().Add(5 * time.Second)
								i.headerCurrent = header
							} else {
								i.log.WithError(err).Warn("unable to read current header from node")
							}
						}
						i.log.Debugf("submitted extrinsic: %+v", frame.Params)

						extr.FirstSeenBlockNo = i.headerCurrent.MustInt("number")
						err = i.db.LogMessage(ctx, &dto.StoredMessage{
							Extrinsic: extr,
							RPCFrame:  frame,
						})
						if err != nil {
							i.log.WithError(err).Errorf("unable to store message %s", frame.Raw)
						}
					}(ctx)
				}
			}
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
