package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/cmd/proxy/handler"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/decoder"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/middleware"
	substrate "github.com/p2p-org/substrate-rpc-proxy/pkg/middleware-substrate"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/rpc"
	endpoint "github.com/p2p-org/substrate-rpc-proxy/pkg/rpc-endpoint"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/storage"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	_ "go.uber.org/automaxprocs"
)

func main() {
	var (
		upstreams *endpoint.UpstreamProvider
		err       error
		cfg       Config
	)
	l := logrus.New()
	err = envconfig.Process("", &cfg)
	if err != nil {
		l.WithError(err).Fatal("unable to load config")
	}
	l.SetOutput(os.Stdout)
	if cfg.LogFormatter == "json" {
		l.SetFormatter(&logrus.JSONFormatter{})
	} else {
		l.SetFormatter(&logrus.TextFormatter{})
	}
	err = l.Level.UnmarshalText([]byte(cfg.LogLevel))
	if err != nil {
		l.WithError(err).Fatalf("unable to read log level from: %s", cfg.LogLevel)
	}

	if cfg.IgnoreHealthchecks {
		upstreams, err = endpoint.NewUpstreamProvider(context.Background(), l, cfg.Hosts, nil)
	} else {
		upstreams, err = endpoint.NewUpstreamProvider(context.Background(), l, cfg.Hosts, substrate.Healthcheck(cfg.UpsteamMinPeers, cfg.UpsteamMaxGap))
	}
	if err != nil {
		l.WithError(err).Fatalf("unable to create upstream provider")
	}

	//jsonRpcLog := &middleware.JsonRPCLog{}
	httpmetrics := middleware.HttpMetrics{}
	proxy := handler.NewProxy(l)
	mon := monitoring.PrometheusMetrics{}

	var extrinsicInsp *substrate.ExtrinsicsInspector
	client := rpc.NewRPCClient(l, upstreams, 10)
	metadataRaw, err := client.StateGetMetadata(context.Background())
	if err != nil {
		l.WithError(err).Fatal("can not read metadata")
	}
	decoder, err := decoder.NewDecoder(l, metadataRaw)
	if err != nil {
		l.WithError(err).Fatal("can not create decoder")
	}

	mon.Register(upstreams)
	mon.Register(proxy)
	mon.Register(&httpmetrics)
	http.Handle("/metrics", promhttp.Handler())
	if cfg.PProfEnabled {
		http.Handle("/debug", chimiddleware.Profiler())
	}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Ok")
	})

	go http.ListenAndServe(cfg.MetricsListen, nil)
	r := chi.NewRouter()
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.EndpointProvider(upstreams))
	r.Use(middleware.AcceptConnection)
	r.Use(middleware.JsonRPC(
		httpmetrics.Middleware(),
		middleware.Logger(l, cfg.LogIncludeParams),
	))
	r.Use(middleware.ThrottleWithOpts(middleware.ThrottleOpts{
		Limit:          cfg.ThrottleLimit,
		BacklogLimit:   cfg.ThrottleBacklogSize,
		BacklogTimeout: 180 * time.Second,
		Observer:       &mon,
	}))

	r.Use(middleware.DenyRPCMethods(cfg.DenyMethods))
	if cfg.InspecExtrinsics {
		db := storage.NewDBClient(l, cfg.RedisStreamName, cfg.RedisAddr, cfg.RedisPassword)
		extrinsicInsp = substrate.NewExtrinsicsInspector(l, db, decoder, client, cfg.InspectExtrinsicsMethods)
		mon.Register(extrinsicInsp)
		r.Use(extrinsicInsp.LogToDB)
	}
	r.Use(substrate.GetStorageJson(l, decoder, client).Serve)
	r.Mount("/", proxy.RPCHandler())

	for _, addr := range cfg.Listen {
		var rpclistener net.Listener
		if cfg.TLSPublicKey != "" {
			cer, err := tls.LoadX509KeyPair(cfg.TLSPublicKey, cfg.TLSPrivateKey)
			if err != nil {
				log.Fatalf("unable to load certificate %s %s: %v", cfg.TLSPublicKey, cfg.TLSPrivateKey, err)
				return
			}
			config := &tls.Config{Certificates: []tls.Certificate{cer}}
			rpclistener, err = tls.Listen("tcp", addr, config)
			if err != nil {
				l.WithError(err).Fatalf("unable to start TLS listener %s", addr)
			}
		} else {
			rpclistener, err = net.Listen("tcp", addr)
			if err != nil {
				l.WithError(err).Fatalf("unable to start listening %s", addr)
			}
		}

		l.Infof("started listener %s", addr)

		rpcserver := &http.Server{
			Handler:           r,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      60 * time.Second,
			ReadHeaderTimeout: 30 * time.Second,
			IdleTimeout:       30 * time.Second,
		}

		go func() {
			l.WithError(rpcserver.Serve(rpclistener)).Error("server stopped")
		}()
		defer Stop(l, rpcserver)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)

	run := cfg
	run.RedisPassword = "+++"
	l.Infof("config running %+v", run)
	signal := fmt.Sprint(<-ch)
	l.Info(fmt.Sprintf("got signal %v. shutting down", signal))
}

func Stop(l *logrus.Logger, server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		l.WithError(err).Errorf("Can't stop %s correctly", server.Addr)
	}
}
