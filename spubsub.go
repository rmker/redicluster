package redicluster

import (
	"context"
	"errors"
	"time"

	"github.com/gomodule/redigo/redis"
)

// ShardedPubSubConn wraps a Conn with convenience API for sharded PubSub.
type ShardedPubSubConn struct {
	cp   *ClusterPool
	conn redis.Conn
}

func ChnSlot(channel ...interface{}) (int, error) {
	slot := -1
	for _, cc := range channel {
		chn, err := redis.String(cc, nil)
		if err != nil {
			return -1, err
		}
		if len(chn) > 0 {
			sl := Slot(chn)
			if slot < 0 {
				slot = sl
			} else if sl != slot {
				return -1, errors.New("channels must be in the same slot")
			}
		}
	}
	return slot, nil
}

// Close closes the connection.
func (c *ShardedPubSubConn) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Subscribe subscribes the connection to the specified channels.
func (c *ShardedPubSubConn) SSubscribe(channel ...interface{}) error {
	slot, err := ChnSlot(channel...)
	if err != nil {
		return err
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.conn, err = c.cp.getRedisConnBySlot(slot)
	if err != nil {
		return err
	}

	if err := c.conn.Send("SSUBSCRIBE", channel...); err != nil {
		return err
	}
	return c.conn.Flush()
}

// SUnsubscribe unsubscribes the connection from the given sharded channels, or from all
// of them if none is given.
func (c *ShardedPubSubConn) SUnsubscribe(channel ...interface{}) error {
	if c.conn == nil {
		return errors.New("nil conn")
	}
	if err := c.conn.Send("SUNSUBSCRIBE", channel...); err != nil {
		return err
	}
	return c.conn.Flush()
}

// Ping sends a PING to the server with the specified data.
//
// The connection must be subscribed to at least one channel or pattern when
// calling this method.
func (c *ShardedPubSubConn) Ping(data string) error {
	if c.conn == nil {
		return errors.New("nil conn")
	}
	if err := c.conn.Send("PING", data); err != nil {
		return err
	}
	return c.conn.Flush()
}

// Receive returns a pushed message as a Subscription, Message, Pong or error.
// The return value is intended to be used directly in a type switch as
// illustrated in the PubSubConn example.
func (c *ShardedPubSubConn) Receive() interface{} {
	if c.conn == nil {
		return errors.New("nil conn")
	}
	return c.receiveInternal(c.conn.Receive())
}

// ReceiveWithTimeout is like Receive, but it allows the application to
// override the connection's default timeout.
func (c *ShardedPubSubConn) ReceiveWithTimeout(timeout time.Duration) interface{} {
	if c.conn == nil {
		return errors.New("nil conn")
	}
	return c.receiveInternal(redis.ReceiveWithTimeout(c.conn, timeout))
}

// ReceiveContext is like Receive, but it allows termination of the receive
// via a Context. If the call returns due to closure of the context's Done
// channel the underlying Conn will have been closed.
func (c *ShardedPubSubConn) ReceiveContext(ctx context.Context) interface{} {
	if c.conn == nil {
		return errors.New("nil conn")
	}
	return c.receiveInternal(redis.ReceiveContext(c.conn, ctx))
}

func (c *ShardedPubSubConn) receiveInternal(replyArg interface{}, errArg error) interface{} {
	reply, err := redis.Values(replyArg, errArg)
	if err != nil {
		return err
	}

	var kind string
	reply, err = redis.Scan(reply, &kind)
	if err != nil {
		return err
	}

	switch kind {
	case "smessage":
		var m redis.Message
		if _, err := redis.Scan(reply, &m.Channel, &m.Data); err != nil {
			return err
		}
		return m
	case "ssubscribe", "sunsubscribe":
		s := redis.Subscription{Kind: kind}
		if _, err := redis.Scan(reply, &s.Channel, &s.Count); err != nil {
			return err
		}
		return s
	case "pong":
		var p redis.Pong
		if _, err := redis.Scan(reply, &p.Data); err != nil {
			return err
		}
		return p
	}
	return errors.New("redigo: unknown pubsub notification")
}
