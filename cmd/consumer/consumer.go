package main

import (
	"context"
	"sync"
	"time"

	"github.com/p2p-org/substrate-rpc-proxy/pkg/dto"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/monitoring"
	"github.com/p2p-org/substrate-rpc-proxy/pkg/rpc"

	"github.com/sirupsen/logrus"
)

type Consumer struct {
	mon         monitoring.Observer
	log         *logrus.Logger
	retryDelay  time.Duration
	InProgress  int
	tryResubmit bool
	rw          sync.Mutex
}

func NewConsumer(l *logrus.Logger, tryResubmit bool, retryDelay time.Duration) *Consumer {
	return &Consumer{
		log:         l,
		retryDelay:  retryDelay,
		InProgress:  0,
		tryResubmit: tryResubmit,
	}
}

func (c *Consumer) GetMonitoringEventTypes() []string {
	return []string{
		monitoring.MetricExtrinsicsBlockGap,
		monitoring.MetricExtrinsicsCount,
		monitoring.MetricExtrinsicsResubmissionsCount,
		monitoring.MetricExtrinsicsExpiredCount,
		monitoring.MetricExtrinsicsInvalidCount,
		monitoring.MetricExtrinsicsAlreadyAddedCount,
		monitoring.MerticConsumerMessagesInProgress,
	}
}

func (c *Consumer) SetObserver(mon monitoring.Observer) {
	c.mon = mon
}

func (c *Consumer) ProcessMessage(wsclient *rpc.RPCClient, message *dto.StoredMessage, ack func()) {
	c.rw.Lock()
	c.InProgress++
	c.rw.Unlock()
	c.mon.ProcessEvent(monitoring.MerticConsumerMessagesInProgress, float64(c.InProgress))
	defer func() {
		c.rw.Lock()
		c.InProgress--
		c.rw.Unlock()
		c.mon.ProcessEvent(monitoring.MerticConsumerMessagesInProgress, float64(c.InProgress))
	}()
	// set retry deadline in blocks for immortal transactions
	if message.Extrinsic.LifetimeInBlocks == 0 {
		message.Extrinsic.LifetimeInBlocks = 1024
	}
	c.mon.ProcessEvent(monitoring.MetricExtrinsicsCount, 1)
	tryAfter := c.retryDelay

	for {
		time.Sleep(tryAfter)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c.rw.Lock()
		currentBlock, err := wsclient.ChainGetBlock(ctx, "")
		c.rw.Unlock()
		if err != nil {
			// retry later
			c.log.WithError(err).Warnf("unable to get latest block for extrinsic %s", message.Extrinsic.Hash)
			tryAfter = 5 * time.Second
			continue
		}
		if currentBlock.Number-message.Extrinsic.FirstSeenBlockNo > message.Extrinsic.LifetimeInBlocks {
			c.log.Warnf("stopped tracking extrinsic: %s current block (%d) greater than deadline block (%d)", message.Extrinsic.Hash, currentBlock.Number, message.Extrinsic.FirstSeenBlockNo+message.Extrinsic.LifetimeInBlocks)
			ack()
			c.mon.ProcessEvent(monitoring.MetricExtrinsicsExpiredCount, 1)
			return
		}
		depth := int(currentBlock.Number - message.Extrinsic.FirstSeenBlockNo)
		b, err := wsclient.FindExtrinsicsBlock(ctx, currentBlock.Hash, message.Extrinsic.Payload, depth)
		if err != nil {
			c.log.WithError(err).Warnf("lookup extrinsic %s in chain failed", message.Extrinsic.Hash)
			tryAfter = 5 * time.Second
			continue
		}
		// block with extrinsic not found
		if b == nil {
			if c.tryResubmit {
				wsresp, err := wsclient.RawRequest(message.RPCFrame.Raw)
				if err != nil {
					c.log.WithError(err).Errorf("resubmission of %s failed, upstream websocket RPC not available", message.Extrinsic.Hash)
					tryAfter = 5 * time.Second
					continue
				}
				c.log.Infof("resubmitted extrinsic %s", message.Extrinsic.Hash)
				c.mon.ProcessEvent(monitoring.MetricExtrinsicsResubmissionsCount, 1)
				if wsresp.Error != nil {
					c.mon.ProcessEvent(monitoring.MetricExtrinsicsInvalidCount, 1)
					c.log.Warnf("upstream response %d: %s", wsresp.Error.Code, wsresp.Error.Message)
					ack()
					return
				}
			}

		} else {
			c.log.Infof("extrinsic %s is in block %d, gap %f", message.Extrinsic.Hash, b.Number, float64(b.Number-message.Extrinsic.FirstSeenBlockNo))
			c.mon.ProcessEvent(monitoring.MetricExtrinsicsBlockGap, float64(b.Number-message.Extrinsic.FirstSeenBlockNo))
			c.mon.ProcessEvent(monitoring.MetricExtrinsicsAlreadyAddedCount, 1)
			ack()
			return
		}
		tryAfter = c.retryDelay
	}
}
