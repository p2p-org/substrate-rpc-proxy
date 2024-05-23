package main

type Config struct {
	LogLevel                 string   `envconfig:"SUB_LOG_LEVEL" default:"trace"`
	LogIncludeParams         bool     `envconfig:"SUB_LOG_PARAMS"`
	LogFormatter             string   `envconfig:"SUB_LOG_FORMAT" default:"text"`
	InspecExtrinsics         bool     `envconfig:"SUB_INSPECT_EXTRINISICS"`
	InspectExtrinsicsMethods []string `envconfig:"SUB_INSPECT_METHODS" default:"author_submitExtrinsic"`
	RedisStreamName          string   `envconfig:"SUB_REDIS_STREAM_NAME" default:"extrinsics"`
	RedisAddr                string   `envconfig:"SUB_REDIS_ADDR" default:"127.0.0.1:6379"`
	RedisPassword            string   `envconfig:"SUB_REDIS_PASSWORD"`
	Listen                   []string `envconfig:"SUB_LISTEN" default:":9944,:9933"`
	TLSPublicKey             string   `envconfig:"SUB_TLS_CERT"`
	TLSPrivateKey            string   `envconfig:"SUB_TLS_KEY"`
	MetricsListen            string   `envconfig:"SUB_METRICS_LISTEN" default:":8888"`
	PProfEnabled             bool     `envconfig:"SUB_PPROF_ENABLED"`
	Hosts                    []string `envconfig:"SUB_HOSTS" default:"http+ws://127.0.0.1:9944/"`
	UpsteamMaxGap            int      `envconfig:"SUB_UPSTREAM_MAXGAP" default:"5"`
	UpsteamMinPeers          int      `envconfig:"SUB_UPSTREAM_MINPEERS" default:"1"`
	ThrottleBacklogSize      int      `envconfig:"SUB_THROTTLE_BACKLOG_SIZE" default:"30000"`
	ThrottleLimit            int      `envconfig:"SUB_THROTTLE_LIMIT" default:"6000"`
	IgnoreHealthchecks       bool     `envconfig:"SUB_IGNORE_HEALTHCHECKS" default:"0"`
	DenyMethods              []string `envconfig:"SUB_DENY_METHODS" default:"author_rotateKeys"`
}
