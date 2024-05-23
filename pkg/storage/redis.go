package storage

import (
	"context"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"

	jsoniter "github.com/json-iterator/go"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type DBClient struct {
	client *redis.UniversalClient
	log    *logrus.Logger
	stream string
}

func NewDBClient(l *logrus.Logger, stream string, address string, password string) *DBClient {
	redis := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        []string{address},
		Password:     password,
		DialTimeout:  30 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		PoolSize:     128,
		MaxRetries:   2,
	})
	//redis.XGroupCreateMkStream(context.Background(), stream, "default", "$").Result()
	return &DBClient{
		client: &redis,
		log:    l,
		stream: stream,
	}
}

func (db *DBClient) LogMessage(ctx context.Context, message *dto.StoredMessage) error {
	var err error
	conn := *db.client
	b, err := jsoniter.Marshal(message)
	if err != nil {
		return err
	}
	_, err = conn.XAdd(ctx, &redis.XAddArgs{
		Stream: db.stream,
		Approx: true,
		MaxLen: 8000,
		Values: []string{"message", string(b)},
	}).Result()
	if err != nil {
		return err
	}
	return nil
}

func (db *DBClient) RegisterConsumer(ctx context.Context, groupName, consumerName string) error {
	var err error
	conn := *db.client
	createGroup := true
	st, _ := conn.XInfoStream(ctx, db.stream).Result()
	if st != nil {
		groupsInfo, err := conn.XInfoGroups(ctx, db.stream).Result()
		if err != nil {
			return err
		}
		for _, g := range groupsInfo {
			if groupName == g.Name {
				createGroup = false
				break
			}
		}
	}
	if createGroup {
		_, err = conn.XGroupCreateMkStream(ctx, db.stream, groupName, "0-0").Result()
		if err != nil {
			return err
		}
	}
	createConsumer := true
	consumersInfo, err := conn.XInfoConsumers(ctx, db.stream, groupName).Result()
	if err != nil {
		return err
	}
	for _, c := range consumersInfo {
		if consumerName == c.Name {
			createConsumer = false
			break
		}
	}
	if createConsumer {
		_, err = conn.XGroupCreateConsumer(ctx, db.stream, groupName, consumerName).Result()
		if err != nil {
			return err
		}
	}
	return nil
}

func (db *DBClient) ReadPendingMessages(ctx context.Context, groupName, consumerName string, dur time.Duration) ([]string, []*dto.StoredMessage, error) {
	var err error
	conn := *db.client
	numPendingMessages := conn.XPending(context.Background(), db.stream, groupName).Val().Count
	pending, err := conn.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream:   db.stream,
		Group:    groupName,
		Idle:     dur,
		Start:    "-",
		End:      "+",
		Count:    numPendingMessages,
		Consumer: consumerName,
	}).Result()
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	var msgs []*dto.StoredMessage
	for _, p := range pending {
		if len(conn.XRange(ctx, db.stream, p.ID, p.ID).Val()) == 1 {
			rawMessage := conn.XRange(ctx, db.stream, p.ID, p.ID).Val()[0].Values["message"].(string)
			msg := dto.StoredMessage{}
			jsoniter.Unmarshal([]byte(rawMessage), &msg)
			msgs = append(msgs, &msg)
			ids = append(ids, p.ID)
		}
	}

	return ids, msgs, nil
}

func (db *DBClient) Consume(ctx context.Context, groupName, consumerName string) (string, *dto.StoredMessage, error) {
	var err error
	conn := *db.client
	streams, err := conn.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    groupName,
		Consumer: consumerName,
		Streams:  []string{db.stream, ">"},
		Count:    1,
		NoAck:    true, // ignore ACK
	}).Result()
	if err != nil {
		return "", nil, err
	}
	if len(streams) >= 1 {
		if len(streams[0].Messages) >= 1 {
			m := streams[0].Messages[0]
			rawMessage := m.Values["message"].(string)
			msg := dto.StoredMessage{}
			if err = jsoniter.Unmarshal([]byte(rawMessage), &msg); err != nil {
				return "", nil, err
			}
			return m.ID, &msg, err
		}
	}
	return "", nil, err
}

func (db *DBClient) AckMessage(ctx context.Context, groupName string, messageID string) error {
	var err error
	conn := *db.client
	_, err = conn.XAck(ctx, db.stream, groupName, messageID).Result()
	if err != nil {
		return err
	}
	return nil
}
