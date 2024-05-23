package substrate

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"

	jsoniter "github.com/json-iterator/go"
)

func Healthcheck(minPeers int, maxGap int) endpoint.UpstreamHealthcheckFunc {
	return func(ctx context.Context, server string) (endpoint.EndpointStatus, error) {

		client := &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     10 * time.Second,
				Dial: (&net.Dialer{
					Timeout: 10 * time.Second,
				}).Dial,
			},
		}
		var (
			v   dto.RPCFrame
			err error
		)

		parts := strings.Split(server, "://")
		if len(parts) > 1 {
			if strings.Contains(parts[0], "https") {
				parts[0] = "https"
			} else {
				parts[0] = "http"
			}
			server = strings.Join(parts, "://")
		}

		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		health, _ := http.NewRequest("POST", server, bytes.NewBufferString(`{"id":11,"jsonrpc":"2.0","method":"system_health","params":[] }`))
		health = health.WithContext(ctx)
		health.Header.Set("Content-Type", "application/json; charset=UTF-8")

		sync, _ := http.NewRequest("POST", server, bytes.NewBufferString(`{"id":13,"jsonrpc":"2.0","method":"system_syncState","params":[]}`))
		sync = sync.WithContext(ctx)
		sync.Header.Set("Content-Type", "application/json; charset=UTF-8")
		healthresp, err := client.Do(health)
		if err != nil {
			return endpoint.EndpointStatusUnreachable, err
		}
		defer healthresp.Body.Close()
		rawhealth, err := io.ReadAll(healthresp.Body)
		if err != nil {
			return endpoint.EndpointStatusUnreachable, err
		}

		syncresp, err := client.Do(sync)
		if err != nil {
			return endpoint.EndpointStatusUnreachable, err
		}
		defer syncresp.Body.Close()
		rawsync, err := io.ReadAll(syncresp.Body)
		if err != nil {
			return endpoint.EndpointStatusUnreachable, err
		}
		err = jsoniter.Unmarshal(rawhealth, &v)
		if err != nil {
			return endpoint.EndpointStatusAvailable, err
		}
		peers := v.MappedResult().MustInt("peers")

		err = jsoniter.Unmarshal(rawsync, &v)
		if err != nil {
			return endpoint.EndpointStatusAvailable, err
		}

		currentBlock := v.MappedResult().MustInt("currentBlock")
		highestBlock := v.MappedResult().MustInt("highestBlock")

		if peers >= uint64(minPeers) && highestBlock != 0 && highestBlock-currentBlock <= uint64(maxGap) {
			return endpoint.EndpointStatusHealthy, nil
		}

		return endpoint.EndpointStatusAvailable, nil
	}
}
