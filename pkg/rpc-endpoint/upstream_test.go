package endpoint

import (
	"context"
	"net"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestUpstreamStatusEndpointProvider(t *testing.T) {
	usp, _ := NewUpstreamProvider(context.Background(), logrus.New(), []string{"localhost:9944"}, nil)
	t.Log(usp.upstreams["localhost"])
	if len(usp.upstreams["localhost"].endpoints) < 1 {
		t.Fatalf("hostname was not resolved")
	}
	if usp.upstreams["localhost"].endpoints[0].address.String() != "127.0.0.1" {
		t.Fatalf("resolved address is wrong expected %s, got %s", "127.0.0.1", usp.upstreams["localhost"].endpoints[0].address.String())
	}
	usp.upstreams["localhost"].updateHostIPs([]net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("127.0.0.2"),
		net.ParseIP("127.0.0.3"),
	})
	if len(usp.upstreams["localhost"].endpoints) != 3 {
		t.Fatalf("expected new endpoints len 3, got %d", len(usp.upstreams["localhost"].endpoints))
	}
	usp.upstreams["localhost"].updateHostIPs([]net.IP{
		net.ParseIP("127.0.0.2"),
		net.ParseIP("127.0.0.3"),
		net.ParseIP("127.0.0.4"),
	})
	if usp.upstreams["localhost"].endpoints[2].address.String() != "127.0.0.4" {
		t.Fatalf("expected new endpoint to be 127.0.0.4, got %s", usp.upstreams["localhost"].endpoints[2].address.String())
	}

	usp.upstreams["localhost"].ResolveDNSRecord(context.Background())
	if usp.upstreams["localhost"].endpoints[0].address.String() != "127.0.0.1" {
		t.Fatalf("resolv does not overwrite manually set address: expected %v, got %v", "127.0.0.1", usp.upstreams["localhost"].endpoints[0].address.String())
	}
}

func TestParser(t *testing.T) {
	ssl, _ := ParseUpstreamAddr("https://a.b.c.com")
	if ssl.MustResolve != false {
		t.Fatal("must not event try resolve https urls")
	}
	if ssl.Ports["https"] != "" {
		t.Fatal("port must be empty if not present in source record")
	}
	combined, _ := ParseUpstreamAddr("http+ws://node1:7777/rpc")
	if combined.Ports["http"] != "7777" || combined.Ports["ws"] != "7777" || len(combined.Ports) != 2 {
		t.Fatalf("%v must be {http: 7777, ws: 7777}", combined.Ports)
	}

	simple, _ := ParseUpstreamAddr("127.0.0.1:7777")
	if simple.Ports["http"] != "7777" || simple.Ports["ws"] != "7777" || len(simple.Ports) != 2 {
		t.Fatalf("%v simple record does not produce ports", simple.Ports)
	}

	simpledns, _ := ParseUpstreamAddr("node1:7777")
	if simpledns.MustResolve == false {
		t.Fatalf("%v MustResolve not set", simpledns)
	}
}
