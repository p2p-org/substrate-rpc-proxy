#### Substrate-RPC-Proxy


JSON-RPC/substrate aware reverse proxy in Go. 


Conisist of two parts:

`proxy` - simple reverse proxy supports websocket/http
`consumer` (optional) - monitors extrinsics (seen by proxy) inclusion to blockchain, provide observability and retry mechanism to resist node failures after transaction was accepted by RPC node but before it was included into block


##### Proxy

* `SUB_LISTEN (default ":9944,:9933")` -  Comma-separated list of local addresses to listen
* `SUB_HOSTS (default "http+ws://127.0.0.1:9944/")` - Comma-separated list of upstream urls, each upstream schemes supported: ws, http, http+ws, https, wss, https+wss
* `SUB_UPSTREAM_MAXGAP (default 5)`, `SUB_UPSTREAM_MINPEERS (default 1)` - Upstream healthness parameters, see "How Proxy choose upstream" section for more details
* `SUB_TLS_CERT`, `SUB_TLS_KEY` - path to TLS/https keypair if `SUB_TLS_*` specified proxy will start in https mode, consider changing `SUB_LISTEN` to 443. *Disclaimer: although proxy can offload https it is still better to use CDN in production environments*
* `SUB_LOG_LEVEL (default "trace")` - Will log each rpc request and server-side push possible values: "trace","debug","info","warning","error","fatal","panic"
* `SUB_LOG_PARAMS (default false)` - Toggle JSON frame "params" logging
* `SUB_LOG_FORMAT (default "text")` - Can be switched to "json"
* `SUB_INSPECT_EXTRINISICS (default false)` - Informs proxy to check incomming requests for extrinsics and post extrinsics to redis stream
* `SUB_INSPECT_METHODS (default author_submitExtrinsic)` - Comma-separated list of RPC methods to inspec extrinsics (does not make sense if `SUB_INSPECT_EXTRINISICS: false`)
* `SUB_REDIS_STREAM_NAME`, `SUB_REDIS_ADDR`, `SUB_REDIS_PASSWORD` - Redis connection settings (does not make sense if `SUB_INSPECT_EXTRINISICS: false`)
* `SUB_METRICS_LISTEN (default ":8888")` - Separate binding for exposing prometheus metrics
* `SUB_PPROF_ENABLED (default false)` - Enable /debug/pprof/ hadnler on `SUB_METRICS_LISTEN`
* `SUB_THROTTLE_BACKLOG_SIZE (default 30000)`, `SUB_THROTTLE_LIMIT (default 6000)` - Rate-limiting parameters if `SUB_THROTTLE_LIMIT` reached requests proxy will queue 26k `SUB_THROTTLE_BACKLOG_SIZE - SUB_THROTTLE_LIMIT` more requests for 3 minutes max to 
* `SUB_IGNORE_HEALTHCHECKS` - Removes upstream healthchecking logic considering that it's always alive 
* `SUB_DENY_METHODS (default "author_rotateKeys")` - Comma-separated list of methods that proxy must not forward to upstream 

##### How Proxy choose upstream

Only one load-balancing strategy supported: **random**

1. If `SUB_HOSTS` contains DNS records and upstream does not support TLS then hosts will be resolved to IP and future subrequests will be performed against IP, it will continue track DNS changes every 10 seconds. In other case (TLS+DNS name) each new connection IP will be resolved on OS level.
2. proxy polls `system_health` and `system_syncState` of each upstream to identify status
3. *healthy* upstream must meet following conditions:
    * `system_syncState.highestBlock - system_syncState.currentBlock < SUB_UPSTREAM_MAXGAP`
    * `system_health.peers > SUB_UPSTREAM_MINPEERS`
4. if these is no *healthy* upsteam, then upsteam will be picked from servers that able to handle websocket connection, does not considering block gap and number of peers.
5. if there is no server that can handle websocket request then proxy will wait 10 seconds and then retry to pick upstream and finally proxy will giveup and forward request to **random** server if no apropriate was found.


##### Consumer

Tracks extrinsic inclusion and optionally tries to resubmit them, configuration:

`SUB_REDIS_CONSUMER_NAME`, `SUB_REDIS_STREAM_NAME`, `SUB_REDIS_ADDR`, `SUB_REDIS_CONSUMER_GROUP`, `SUB_HOSTS`, `SUB_METRICS_LISTEN`, `SUB_UPSTREAM_MAXGAP`, `SUB_UPSTREAM_MINPEERS` - same parameters as for proxy.

Worth mentioning:
* `SUB_RETRY_DELAY` - how often consumer should check for extrinsic inclusion 
* `SUB_TRY_RESUBMIT` - resubmit extrinsic if it not appeared on-chain so far


##### Similar projects

* [dshackle](https://github.com/emeraldpay/dshackle)
* [subway](https://github.com/acalanetwork/subway) 


##### Features

Decode storage with any http client 
```curl -s -d '{"id":1, "jsonrpc":"extensions/get-storage/1.0","method":"system.events"}' -v http://127.0.0.1:9944 | jq
[
  {
    "type": {
      "module_id": "System",
      "event_id": "ExtrinsicSuccess"
    },
    "extrinsic_idx": 0,
    "params": [...]
  },
  {
    "type": {
      "module_id": "ParaInclusion",
      "event_id": "CandidateIncluded"
    }
   ...
```

##### History

Project started in Jan 2023 in p2p.org in Polkadot SRE Team. Main goal of the project is to provide stable RPC service.


##### References

github.com/itering/scale.go - Go implementation of scale codec
