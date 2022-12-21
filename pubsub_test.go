package redicluster

import (
	"reflect"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
)

func expectPushed(t *testing.T, c *redis.PubSubConn, message string, expected interface{}) {
	actual := c.Receive()
	t.Logf("expectPushed actual= %v", actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("%s = %v, want %v", message, actual, expected)
	}
}

func expectPushedSharded(t *testing.T, c *ShardedPubSubConn, message string, expected interface{}) {
	actual := c.Receive()
	t.Logf("expectPushedSharded actual= %v", actual)
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

func TestShardedPubSub(t *testing.T) {
	cp := WithPool(t)
	p := cp.Get()
	defer p.Close()

	s1, err := cp.GetShardedPubSubConn()
	defer s1.Close()
	assert.NoError(t, err, "GetPubSubConn for subscriber 1 failed")

	s2, err := cp.GetShardedPubSubConn()
	defer s2.Close()
	assert.NoError(t, err, "GetPubSubConn for subscriber 2 failed")

	s1.SSubscribe("ShardedChnA")
	expectPushedSharded(t, s1, "s1.SSubscribe", redis.Subscription{Kind: "ssubscribe", Channel: "ShardedChnA", Count: 1})

	s2.SSubscribe("ShardedChnA")
	expectPushedSharded(t, s2, "s2.SSubscribe", redis.Subscription{Kind: "ssubscribe", Channel: "ShardedChnA", Count: 1})

	content := "I'm sharded msg"

	p.Do("SPUBLISH", "ShardedChnA", content)

	expectPushedSharded(t, s1, "s1.SSubscribe", redis.Message{Channel: "ShardedChnA", Data: []byte(content)})
	expectPushedSharded(t, s2, "s2.SSubscribe", redis.Message{Channel: "ShardedChnA", Data: []byte(content)})
}
