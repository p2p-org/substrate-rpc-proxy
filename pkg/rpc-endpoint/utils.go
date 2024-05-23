package endpoint

import (
	"fmt"
	"net/url"
)

func FormatUpstream(some interface{}) string {
	var host, port, path string
	switch v := some.(type) {
	case *Upstream:
		port = v.Addr.Port()
		host = v.Addr.Hostname()
		path = v.Addr.Path
	case *UpstreamEndpoint:
		port = v.upstream.Addr.Port()
		host = v.address.String()
		path = v.upstream.Addr.Path
	case string:
		u, err := url.Parse(v)
		if err != nil {
			host = v
		} else {
			port = u.Port()
			host = u.Hostname()
			path = u.Path
		}

	}
	if port != "" {
		host = fmt.Sprintf("%s:%s", host, port)
	}
	if path != "" {
		host = fmt.Sprintf("%s%s", host, path)
	}
	return host
}
