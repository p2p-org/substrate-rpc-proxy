package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/rpc"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"

	"github.com/sirupsen/logrus"
)

// this is not a test just a snippet to run queries
//
//	func TestContextCancelation(t *testing.T) {
//		wsclient := rpc.NewRPCClient(logrus.New(), endpoint.NewAlwaysAliveEndpoint("ws://127.0.0.1:9944"), 10)
//		b, err := wsclient.ChainGetHeader(context.Background(), "")
//		t.Log(b, err)
//		time.Sleep(25 * time.Second)
//	}

func Test(t *testing.T) {
	ctx := context.Background()
	wsclient := rpc.NewRPCClient(logrus.New(), endpoint.NewAlwaysAliveEndpoint("ws://127.0.0.1:9944/"), 10)
	ch, errs := wsclient.ChainSubscribeJustifications(ctx)
	for {
		select {
		case f := <-ch:
			t.Log(f)
		case err := <-errs:
			t.Fatalf("unable to subscribe %v", err)
			return
		}
	}
}
func TestGetRuntimeVersion(t *testing.T) {
	var w sync.WaitGroup
	for j := 0; j < 100; j++ {
		go func(b int) error {
			wsclient := rpc.NewRPCClient(logrus.New(), endpoint.NewAlwaysAliveEndpoint("ws://127.0.0.1:9944/"), 10)
			beg := time.Now()
			numBlocks := 1000
			for i := b * numBlocks; i < (b+1)*numBlocks; i++ {
				ctx := context.Background()
				resp, _ := wsclient.ChainGetBlockHash(ctx, uint64(i))
				wsclient.RawRequest(fmt.Sprintf(`{"id":%d,"jsonrpc":"2.0","method":"state_getRuntimeVersion","params":["%s"]}`, i, resp))
			}
			fmt.Printf("reading %d blocks, took: %fs\n", numBlocks, time.Since(beg).Seconds())
			defer w.Done()
			return nil
		}(j)
		w.Add(1)
	}
	w.Wait()
}
