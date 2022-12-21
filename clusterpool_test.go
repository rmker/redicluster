package redicluster

import (
	"context"
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
