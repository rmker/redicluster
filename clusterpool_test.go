package redicluster

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// To run these testing, you should launch a local redis cluster in advance

func WithoutPool(t *testing.T) *ClusterPool {
	cp := &ClusterPool{
		EntryAddrs: []string{"127.0.0.1:6379"},
	}
	err := cp.ReloadSlotMapping()
	assert.NoError(t, err, "ReloadSlotMapping failed")
	return cp
}

func WithPool(t *testing.T) *ClusterPool {
	createConnPool := func(ctx context.Context, addr string) (*redis.Pool, error) {
		return &redis.Pool{
			Dial: func() (redis.Conn, error) {
				return redis.Dial(
					"tcp",
					addr,
					redis.DialWriteTimeout(time.Second*3),
					redis.DialConnectTimeout(time.Second*3),
					redis.DialReadTimeout(time.Second*3))
			},
			DialContext: func(ctx context.Context) (redis.Conn, error) {
				return redis.DialContext(
					ctx,
					"tcp",
					addr,
					redis.DialWriteTimeout(time.Second*3),
					redis.DialConnectTimeout(time.Second*3),
					redis.DialReadTimeout(time.Second*3))
			},
			TestOnBorrow: func(c redis.Conn, t time.Time) error {
				if time.Since(t) > time.Minute {
					_, err := c.Do("PING")
					return err
				}
				return nil
			},
			MaxIdle:     10,
			MaxActive:   10,
			IdleTimeout: time.Minute * 10,
		}, nil
	}
	cp := &ClusterPool{
		EntryAddrs:     []string{"127.0.0.1:6379"},
		CreateConnPool: createConnPool,
	}
	err := cp.ReloadSlotMapping()
	assert.NoError(t, err, "ReloadSlotMapping failed")
	return cp
}

func TestConnectWithoutPool(t *testing.T) {
	cp := WithoutPool(t)
	conn := cp.Get()
	defer conn.Close()
	rep, err := conn.Do("PING")
	require.NoError(t, err)
	t.Logf("ping:%s", rep)
}

func TestReloadSlots(t *testing.T) {
	cp := WithPool(t)
	t.Logf("slots:\n%s", cp.VerbosSlotMapping())
}

func TestNormalCommand(t *testing.T) {
	cp := WithPool(t)
	conn := cp.Get()
	defer conn.Close()
	rep, err := conn.Do("SET", "abc", "123")
	assert.NoError(t, err)
	t.Logf("set result:%s", rep)

	rep, err = conn.Do("GET", "abc")
	assert.NoError(t, err)
	t.Logf("get result:%s", rep)

	t.Logf("active: %d, idle: %d", cp.ActiveCount(), cp.IdleCount())
}

func expectPushed(t *testing.T, c *redis.PubSubConn, message string, expected interface{}) {
	actual := c.Receive()
	t.Logf("expectPushed actual= %v", actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("%s = %v, want %v", message, actual, expected)
	}
}

func TestPubSub(t *testing.T) {
	cp := WithPool(t)
	p := cp.Get()
	defer p.Close()

	s1, err := cp.GetPubSubConn()
	defer s1.Close()
	assert.NoError(t, err, "GetPubSubConn for subscriber 1 failed")

	s2, err := cp.GetPubSubConn()
	defer s2.Close()
	assert.NoError(t, err, "GetPubSubConn for subscriber 2 failed")

	s1.Subscribe("ChnA")
	expectPushed(t, s1, "s1.Subscribe", redis.Subscription{Kind: "subscribe", Channel: "ChnA", Count: 1})

	s2.Subscribe("ChnA")
	expectPushed(t, s2, "s2.Subscribe", redis.Subscription{Kind: "subscribe", Channel: "ChnA", Count: 1})

	content := "I'm msg"

	p.Do("PUBLISH", "ChnA", content)

	expectPushed(t, s1, "s1.Subscribe", redis.Message{Channel: "ChnA", Data: []byte(content)})
	expectPushed(t, s2, "s2.Subscribe", redis.Message{Channel: "ChnA", Data: []byte(content)})
}
