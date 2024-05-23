package substrate

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/decoder"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/rpc"

	"github.com/go-chi/render"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
)

type GetStorageExtension struct {
	client   *rpc.RPCClient
	decoder  *decoder.Decoder
	log      *logrus.Logger
	methodRe regexp.Regexp
}

func GetStorageJson(l *logrus.Logger, dec *decoder.Decoder, client *rpc.RPCClient) *GetStorageExtension {
	return &GetStorageExtension{
		client:   client,
		log:      l,
		decoder:  dec,
		methodRe: *regexp.MustCompile(`(?P<module>\w+).(?P<method>\w+)`),
	}
}

func (st *GetStorageExtension) Serve(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frame := middleware.GetJsonRPCFrame(r.Context())
		if frame != nil && frame.RPC == "extensions/get-storage/1.0" {
			matches := st.methodRe.FindStringSubmatch(frame.Method)
			if len(matches) != 3 {
				render.Render(w, r, middleware.ErrorInvalidRequest(fmt.Errorf("unable to parse %s", frame.Method)))
				return
			}
			module := matches[st.methodRe.SubexpIndex("module")]
			method := matches[st.methodRe.SubexpIndex("method")]
			storageReq, err := st.decoder.NewStorageRequest(module, method, frame.Params)
			if err != nil {
				render.Render(w, r, middleware.ErrorInvalidRequest(fmt.Errorf("no such method %s.%s", module, method)))
				return
			}

			resp, err := st.client.StateGetStorage(r.Context(), storageReq, "")
			if err != nil {
				render.Render(w, r, middleware.ErrorInternalServer(fmt.Errorf("get storage error %v", err)))
				return
			}
			if strings.EqualFold(module, "system") && strings.EqualFold(method, "events") {
				events := st.decoder.DecodeEvents(r.Context(), resp)
				b, _ := jsoniter.Marshal(events)
				w.Write(b)
			} else {
				structResp := storageReq.DecodeResponse(resp)
				b, _ := jsoniter.Marshal(structResp)
				w.Write(b)
			}
		} else {
			next.ServeHTTP(w, r)
		}
	})
}
