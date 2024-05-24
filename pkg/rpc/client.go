package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/decoder"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"

	"github.com/sirupsen/logrus"
	"nhooyr.io/websocket"
)

const (
	ServerKindWS  = "websocket"
	ServerKindRPC = "http"
)

type contextKey string

const (
	contextKeyRPCCallId  = contextKey("wsconnRequestID")
	contextKeyBrokerName = contextKey("wsconnID")
)

type RPCClient struct {
	blockCache        *BlockCache
	endpointProvider  endpoint.Provider
	clientTimeout     time.Duration
	log               *logrus.Logger
	defaultConnection string
	connections       sync.Map //map[string]*WSMessageBroker
	rw                sync.Mutex
}

func NewRPCClient(log *logrus.Logger, endpoints endpoint.Provider, blockCacheSize int) *RPCClient {
	c := RPCClient{
		endpointProvider: endpoints,
		clientTimeout:    60 * time.Second,
		log:              log,
		blockCache: &BlockCache{
			blocks:   []dto.Block{},
			capacity: blockCacheSize,
		},
		defaultConnection: "default",
	}
	return &c
}

func (w *RPCClient) StateGetMetadata(ctx context.Context) (string, error) {
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		return "", err
	}
	if resp, err := conn.WSRequest(ctx, &dto.RPCFrame{Method: "state_getMetadata"}); err == nil {
		return resp.StringResult(), nil
	} else {
		return "", err
	}
}

// TODO: rewrite
func (w *RPCClient) ChainGetBlock(ctx context.Context, hash string) (*dto.Block, error) {

	var (
		resp *dto.RPCFrame
		err  error
	)
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		if header, err := w.ChainGetHeader(ctx, ""); err == nil {
			if block, ok := w.blockCache.Get(header.MustInt("number")); ok {
				return block, nil
			}
			hash, err = w.ChainGetBlockHash(ctx, header.MustInt("number"))
			if err != nil {
				return nil, err
			}
		}
	}
	block, found := w.blockCache.GetByHash(hash)
	if found {
		return block, nil
	}
	resp, err = conn.WSRequest(ctx, &dto.RPCFrame{Method: "chain_getBlock", Params: []string{hash}})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%v", resp.Error)
	}
	b := dto.Block{
		Number:     resp.MappedResult().Get("block").Get("header").MustInt("number"),
		ParentHash: resp.MappedResult().Get("block").Get("header").MustString("parentHash"),
		Extrinsics: resp.MappedResult().Get("block").MustStrings("extrinsics"),
		Hash:       hash,
	}
	w.blockCache.Add(b)
	return &b, nil
}

func (w *RPCClient) ChainGetHeader(ctx context.Context, hash string) (dto.Mapped, error) {
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		return nil, err
	}

	var params []string
	if hash != "" {
		params = []string{hash}
	}
	if resp, err := conn.WSRequest(ctx, &dto.RPCFrame{Method: "chain_getHeader", Params: params}); err == nil {
		return resp.MappedResult(), err
	} else {
		return dto.Mapped{}, err
	}
}

func (w *RPCClient) ChainGetBlockHash(ctx context.Context, num uint64) (string, error) {
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		return "", err
	}
	if v, err := conn.WSRequest(ctx, &dto.RPCFrame{Method: "chain_getBlockHash", Params: []uint64{num}}); err == nil {
		return v.StringResult(), nil
	} else {
		return "", err
	}
}

func (w *RPCClient) StateGetStorage(ctx context.Context, request *decoder.StorageRequest, blockHash string) (string, error) {
	var params []string
	if blockHash != "" {
		params = []string{request.StorageKey, blockHash}
	} else {
		params = []string{request.StorageKey}
	}
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		return "", err
	}
	if v, err := conn.WSRequest(ctx, &dto.RPCFrame{Method: "state_getStorage", Params: params}); err == nil { // panic
		return v.StringResult(), nil
	} else {
		return "", err
	}
}

func (w *RPCClient) ChainSubscribeNewHead(ctx context.Context) (chan *dto.RPCFrame, chan error) {
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		e := make(chan error, 1)
		e <- err
		return nil, e
	}
	return conn.Subscribe(ctx, &dto.RPCFrame{Method: "chain_subscribeNewHead"})
}

func (w *RPCClient) ChainSubscribeJustifications(ctx context.Context) (chan *dto.RPCFrame, chan error) {
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		e := make(chan error, 1)
		e <- err
		return nil, e
	}
	return conn.Subscribe(ctx, &dto.RPCFrame{Method: "grandpa_subscribeJustifications"})
}

func (w *RPCClient) FindExtrinsicsBlock(ctx context.Context, blockHash string, extrinsic string, limit int) (*dto.Block, error) {
	maxDepth := limit
	if limit > w.blockCache.capacity {
		maxDepth = w.blockCache.capacity
	}
	for i := 0; i < maxDepth; i++ {
		b, err := w.ChainGetBlock(ctx, blockHash)
		if err != nil {
			return nil, err
		}
		for _, e := range b.Extrinsics {
			if e == extrinsic {
				return b, nil
			}
		}
		blockHash = b.ParentHash
	}
	// block with extrinsic was not found
	return nil, nil
}

// Returns context that can be used later for RPC requests
func (w *RPCClient) NewConnectionContext(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, contextKeyBrokerName, name)
}

func (w *RPCClient) ReleaseConnection(ctx context.Context) {
	if ctx.Value(contextKeyBrokerName) == nil {
		ctx = context.WithValue(ctx, contextKeyBrokerName, w.defaultConnection)
	}
	w.rw.Lock()
	defer w.rw.Unlock()
	if name := ctx.Value(contextKeyBrokerName); name != nil {
		if conn, ok := w.connections.LoadAndDelete(name.(string)); ok {
			conn := conn.(*WSMessageBroker)
			w.closeWSConnection(conn.ws, conn.err)
		}
	}
}

func (w *RPCClient) GetOrCreateConnection(ctx context.Context) (*WSMessageBroker, error) {

	if ctx.Value(contextKeyBrokerName) == nil {
		ctx = context.WithValue(ctx, contextKeyBrokerName, w.defaultConnection)
	}
	var name string
	if n := ctx.Value(contextKeyBrokerName); n != nil {
		name = n.(string)
	}

	w.rw.Lock()
	defer w.rw.Unlock()
	if conn, ok := w.connections.Load(name); ok {
		conn := conn.(*WSMessageBroker)
		if conn.err == nil {
			return conn, nil
		}
		// reset exsiting connection in case of errors
		w.log.WithError(conn.err).Warnf("connection [%s] has failed and will be recreated", name)
		w.closeWSConnection(conn.ws, conn.err)
		w.connections.Delete(name)
	}
	// TODO: Get rid of context.Background() in favor of ctx that Client/Broker was created with
	c, err := w.openWSConnection(context.Background(), "")
	if err != nil {
		return nil, err
	}

	conn := NewWSMessageBroker(context.Background(), w.log, c)
	w.connections.Store(name, conn)
	return conn, nil
}

func (w *RPCClient) GetAliveServer(kind string) string {
	return w.endpointProvider.GetAliveEndpoint(kind, 1)
}

func (w *RPCClient) WithConnection(ctx context.Context, server string, do func(*websocket.Conn) error) error {
	var err error
	c, err := w.openWSConnection(ctx, server)
	if err != nil {
		return err
	}
	err = do(c)
	if err != nil {
		w.closeWSConnection(c, err)
		return err
	}
	w.closeWSConnection(c, nil)
	return nil
}

func (w *RPCClient) RawRequest(msg string) (*dto.RPCFrame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := w.GetOrCreateConnection(ctx)
	if err != nil {
		return nil, err
	}
	frame, err := dto.NewFrameFrom([]byte(msg))
	if err != nil {
		return nil, err
	}
	if v, err := conn.WSRequest(ctx, frame); err == nil {
		return v, nil
	} else {
		return nil, err
	}
}

func (w *RPCClient) openWSConnection(ctx context.Context, server string) (*websocket.Conn, error) {
	if server == "" {
		server = w.GetAliveServer(ServerKindWS)
	}
	c, _, err := websocket.Dial(ctx, server, &websocket.DialOptions{
		HTTPClient: &http.Client{
			Timeout: w.clientTimeout,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     3 * w.clientTimeout,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	c.SetReadLimit(15 * 1024 * 1024)
	return c, nil
}

func (w *RPCClient) closeWSConnection(c *websocket.Conn, err error) {
	if err != nil {
		w.log.WithError(err).Debug("unexpected WS session termination")
	}
	defer c.Close(websocket.StatusInternalError, "")
	err = c.Close(websocket.StatusNormalClosure, "")
	if errors.Is(err, context.Canceled) {
		return
	}
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return
	}
}
