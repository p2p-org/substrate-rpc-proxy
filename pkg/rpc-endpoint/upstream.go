package endpoint

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"

	"github.com/sirupsen/logrus"
)

type UpstreamHealthcheckFunc func(context.Context, string) (EndpointStatus, error)

type UpstreamProvider struct {
	upstreams   map[string]*Upstream
	context     context.Context
	healthCheck UpstreamHealthcheckFunc
	log         *logrus.Logger
	mon         monitoring.Observer
}

type Upstream struct {
	provider    *UpstreamProvider
	endpoints   []UpstreamEndpoint
	status      EndpointStatus
	MustResolve bool
	Addr        *url.URL
	Ports       map[string]string
}
type UpstreamEndpoint struct {
	upstream *Upstream
	address  net.IP
	status   EndpointStatus
}

func getSchemePort(ports map[string]string, kind string) (string, string) {
	var (
		scheme string
		port   string
	)
	switch kind {
	case "websocket":
		if p, ok := ports["wss"]; ok {
			scheme = "wss"
			port = p
		}
		if p, ok := ports["ws"]; ok {
			scheme = "ws"
			port = p
		}
	case "http":
		if p, ok := ports["https"]; ok {
			scheme = "https"
			port = p
		}
		if p, ok := ports["http"]; ok {
			scheme = "http"
			port = p
		}
	default:
		for scheme, port := range ports {
			return scheme, port
		}
	}

	return scheme, port
}

func (u *Upstream) GetAddr(kind string) string {
	host := u.Addr.Hostname()
	scheme, port := getSchemePort(u.Ports, kind)
	if port != "" {
		host = fmt.Sprintf("%s:%s", u.Addr.Hostname(), port)
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, u.Addr.Path)
}

func (e *UpstreamEndpoint) GetAddr(kind string) string {
	host := e.address.String()
	scheme, port := getSchemePort(e.upstream.Ports, kind)
	if port != "" {
		host = fmt.Sprintf("%s:%s", e.address.String(), port)
	}
	return fmt.Sprintf("%s://%s/%s", scheme, host, e.upstream.Addr.Path)
}

func ParseUpstreamAddr(in string) (*Upstream, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if !strings.Contains(in, "://") {
		in = fmt.Sprintf("http+ws://%s", in)
	}
	a, err := url.Parse(in)
	if err != nil {
		return nil, err
	}
	u := Upstream{
		Ports: make(map[string]string),
		Addr:  a,
	}
	switch sc := a.Scheme; sc {
	case "ws", "http", "http+ws", "ws+http":
		if net.ParseIP(a.Hostname()) == nil {
			u.MustResolve = true
			u.ResolveDNSRecord(ctx)
		}
	default:
		u.MustResolve = false
	}
	for _, n := range strings.Split(a.Scheme, "+") {
		if a.Port() != "" {
			u.Ports[n] = a.Port()
		} else {
			u.Ports[n] = ""
		}
	}

	return &u, nil
}

func NewAlwaysAliveEndpoint(url string) *UpstreamProvider {
	prov, err := NewUpstreamProvider(context.Background(), nil, []string{url}, nil)
	if err != nil {
		panic(err)
	}
	return prov
}

func NewUpstreamProvider(ctx context.Context, l *logrus.Logger, hosts []string, hc UpstreamHealthcheckFunc) (*UpstreamProvider, error) {
	provider := UpstreamProvider{
		context:     ctx,
		log:         l,
		healthCheck: hc,
		upstreams:   make(map[string]*Upstream),
	}
	for _, host := range hosts {
		u, err := ParseUpstreamAddr(host)
		if err != nil {
			return nil, err
		}
		u.provider = &provider
		if existing, ok := provider.upstreams[u.Addr.Hostname()]; ok {
			for k, v := range u.Ports {
				existing.Ports[k] = v
			}
		} else {
			provider.upstreams[u.Addr.Hostname()] = u
		}
		go provider.upstreams[u.Addr.Hostname()].CheckContinuously(ctx)
	}
	return &provider, nil
}

func (u *Upstream) updateHostIPs(ips []net.IP) {
	indexToDelete := []int{}
	for i := 0; i < len(u.endpoints); i++ {
		found := false
		for _, ip := range ips {
			if ip.String() == u.endpoints[i].address.String() {
				found = true
			}
		}
		if !found {
			indexToDelete = append([]int{i}, indexToDelete...)
		}
	}
	for _, i := range indexToDelete {
		u.endpoints = append(u.endpoints[:i], u.endpoints[i+1:]...)
	}

	for _, ip := range ips {
		found := false
		for _, e := range u.endpoints {
			if e.address.String() == ip.String() {
				found = true
				break
			}
		}
		if !found {
			u.endpoints = append(u.endpoints, UpstreamEndpoint{
				upstream: u,
				address:  ip,
				status:   EndpointStatusUnreachable,
			})
		}
	}
}

func (u *Upstream) ResolveDNSRecord(ctx context.Context) error {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, u.Addr.Hostname())
	if err != nil {
		return err
	}
	ips := make([]net.IP, len(addrs))
	for i, ia := range addrs {
		ips[i] = ia.IP
	}
	u.updateHostIPs(ips)
	return nil
}

func (u *Upstream) CheckContinuously(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := u.Check(ctx)
		if err != nil {
			u.provider.log.WithError(err).Info(fmt.Sprintf("unhealthy upstream %v", u.Addr.String()))
		}
		time.Sleep(5 * time.Second)
	}
}

func (u *Upstream) Check(ctx context.Context) error {
	var (
		status EndpointStatus
		err    error
	)
	if u.MustResolve {
		err = u.ResolveDNSRecord(ctx)
		if err != nil {
			return err
		}
		for i := 0; i < len(u.endpoints); i++ {
			if u.provider.healthCheck != nil {
				//
				status, eperr := u.provider.healthCheck(ctx, u.endpoints[i].GetAddr(""))
				u.endpoints[i].status = status
				if eperr != nil {
					err = errors.Join(err, eperr)
				}
			} else {
				u.endpoints[i].status = EndpointStatusHealthy
			}

		}
	} else {
		if u.provider.healthCheck != nil {
			status, err = u.provider.healthCheck(ctx, u.GetAddr(""))
			u.status = status
		} else {
			u.status = EndpointStatusHealthy
		}
	}

	return err
}

func (prov *UpstreamProvider) GetStatus(kind string) map[string]EndpointStatus {
	resp := make(map[string]EndpointStatus)
	for _, upst := range prov.upstreams {
		if !upst.MustResolve {
			resp[upst.GetAddr(kind)] = upst.status
		} else {
			for _, ep := range upst.endpoints {
				resp[ep.GetAddr(kind)] = ep.status
			}
		}

	}
	return resp
}

func (prov *UpstreamProvider) GetAliveEndpoint(kind string, tries int) string {
	return prov.GetEndpoint(kind, "", tries)
}

func (prov *UpstreamProvider) TcpCheck(addr string) bool {
	u, _ := url.Parse(addr)
	timeout := time.Second
	conn, _ := net.DialTimeout("tcp", u.Host, timeout)
	if conn != nil {
		defer conn.Close()
		return true
	}
	return false
}

func (prov *UpstreamProvider) GetEndpoint(kind string, exclude string, tries int) string {
	var rating []string
	var all []string
	healthyCount := 0
	availableCount := 0
	// sort keys manually: golang map does not sort keys during range
	keys := []string{}
	for k := range prov.upstreams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// add healthy nodes to begining of the rating, avalilable to the end
	for _, key := range keys {
		upst := prov.upstreams[key]
		if !upst.MustResolve {
			addr := upst.GetAddr(kind)
			if addr == exclude {
				continue
			}
			if upst.status == EndpointStatusHealthy {
				healthyCount++
				availableCount++
				rating = append([]string{addr}, rating...)
			}
			if upst.status == EndpointStatusAvailable {
				availableCount++
				rating = append(rating, addr)
			}
			all = append(all, addr)
		} else {
			for _, ep := range upst.endpoints {
				addr := ep.GetAddr(kind)
				if addr == exclude {
					continue
				}
				if ep.status == EndpointStatusHealthy {
					healthyCount++
					availableCount++
					rating = append([]string{addr}, rating...)
				}
				if ep.status == EndpointStatusAvailable {
					availableCount++
					rating = append(rating, addr)
				}
				all = append(all, addr)
			}
		}
	}

	if healthyCount != 0 {
		return rating[rand.Intn(healthyCount)]
	}
	if availableCount != 0 {
		return rating[rand.Intn(availableCount)]
	}
	if tries > 0 {
		time.Sleep(5 * time.Second)
		return prov.GetAliveEndpoint(kind, tries-1)
	}
	// panic mode, no live endpoints need to return anything
	return all[rand.Intn(len(all))]
}

func (prov *UpstreamProvider) GetMonitoringEventTypes() []string {
	return []string{
		monitoring.MetricUpstreamEndpointsStatus,
	}
}

func (prov *UpstreamProvider) SetObserver(mon monitoring.Observer) {
	prov.mon = mon
	go prov.reportMetrics()
}

func (prov *UpstreamProvider) reportMetrics() {
	for {
		var current []string
		for _, upst := range prov.upstreams {
			if !upst.MustResolve {
				if upst.status == EndpointStatusHealthy {
					current = append(current, fmt.Sprintf("%s|%s", FormatUpstream(upst), "UpNormal"))
					continue
				}
				if upst.status == EndpointStatusAvailable {
					current = append(current, fmt.Sprintf("%s|%s", FormatUpstream(upst), "UpDegraded"))
					continue
				}
				current = append(current, fmt.Sprintf("%s|%s", FormatUpstream(upst), "Unavailable"))
			} else {
				for _, ep := range upst.endpoints {
					if ep.status == EndpointStatusHealthy {
						current = append(current, fmt.Sprintf("%s|%s", FormatUpstream(ep), "UpNormal"))
						continue
					}
					if ep.status == EndpointStatusAvailable {
						current = append(current, fmt.Sprintf("%s|%s", FormatUpstream(ep), "UpDegraded"))
						continue
					}
					current = append(current, fmt.Sprintf("%s|%s", FormatUpstream(ep), "Unavailable"))
				}
			}
		}
		prov.mon.ProcessEvent(monitoring.MetricUpstreamEndpointsStatus, 1, current...)
		time.Sleep(30 * time.Second)
	}
}
