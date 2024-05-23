package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	substrate "github.com/p2p-org/substrate-rpc-proxy/pkg/middleware-substrate"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/rpc"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/storage"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

type Config struct {
	ConsumerGroup   string        `json:"SUB_REDIS_CONSUMER_GROUP" envconfig:"SUB_REDIS_CONSUMER_GROUP" default:"ws-consumer-group"`
	ConsumerName    string        `json:"SUB_REDIS_CONSUMER_NAME" envconfig:"SUB_REDIS_CONSUMER_NAME" default:"ws-consumer-1"`
	RedisStreamName string        `json:"SUB_REDIS_STREAM_NAME" envconfig:"SUB_REDIS_STREAM_NAME" default:"extrinsics"`
	RedisAddr       string        `json:"SUB_REDIS_ADDR" envconfig:"SUB_REDIS_ADDR" default:"127.0.0.1:6379"`
	RedisPassword   string        `json:"-" envconfig:"SUB_REDIS_PASSWORD"`
	Hosts           []string      `json:"SUB_HOSTS" envconfig:"SUB_HOSTS" default:"127.0.0.1"`
	RetryDelay      time.Duration `json:"SUB_RETRY_DELAY" envconfig:"SUB_RETRY_DELAY" default:"60s"`
	MetricsListen   string        `json:"SUB_METRICS_LISTEN" envconfig:"SUB_METRICS_LISTEN" default:":8888"`
	UpsteamMaxGap   int           `json:"SUB_UPSTREAM_MAXGAP" envconfig:"SUB_UPSTREAM_MAXGAP" default:"5"`
	UpsteamMinPeers int           `json:"SUB_UPSTREAM_MINPEERS" envconfig:"SUB_UPSTREAM_MINPEERS" default:"1"`
	TryResubmit     bool          `json:"SUB_TRY_RESUBMIT" envconfig:"SUB_TRY_RESUBMIT"`
}

func main() {
	l := logrus.New()
	l.SetOutput(os.Stdout)
	l.SetFormatter(&logrus.TextFormatter{})
	var cfg Config
	var err error
	err = envconfig.Process("", &cfg)
	if err != nil {
		l.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	upstreamEndpointProvider, err := endpoint.NewUpstreamProvider(context.Background(), l, cfg.Hosts, substrate.Healthcheck(cfg.UpsteamMinPeers, cfg.UpsteamMaxGap))
	if err != nil {
		l.WithError(err).Fatalf("unable to create upstream provider")
	}
	wsclient := rpc.NewRPCClient(l, upstreamEndpointProvider, 1024)
	db := storage.NewDBClient(l, cfg.RedisStreamName, cfg.RedisAddr, cfg.RedisPassword)
	err = db.RegisterConsumer(ctx, cfg.ConsumerGroup, cfg.ConsumerName)
	if err != nil {
		l.WithError(err).Fatal("unable to register consumer")
	}
	consumer := NewConsumer(l, cfg.TryResubmit, cfg.RetryDelay)
	mon := monitoring.PrometheusMetrics{}
	mon.Register(upstreamEndpointProvider)
	mon.Register(consumer)
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Ok")
	})
	go http.ListenAndServe(cfg.MetricsListen, nil)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for {
			id, msg, err := db.Consume(ctx, cfg.ConsumerGroup, cfg.ConsumerName)
			if err != nil {
				l.WithError(err).Error("unable to read stream")
				time.Sleep(30 * time.Second)
				continue
			}
			for consumer.InProgress > 1000 {
				time.Sleep(5 * time.Second)
				l.Infof("consumer is busy %d in progress", consumer.InProgress)
			}
			l.Infof("new incoming message from redis %s", msg.Extrinsic.Hash)
			go consumer.ProcessMessage(wsclient, msg, func() {
				db.AckMessage(ctx, cfg.ConsumerGroup, id)
			})
		}
	}()
	<-ch
}
