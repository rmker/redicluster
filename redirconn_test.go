package redicluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
)

func TestPipeLine1(t *testing.T) {
	cp := WithPool(t)
	conn := cp.Get()
	defer conn.Close()
	n := 6

	// set
	for i := 0; i < n; i++ {
		err := conn.Send("SET", fmt.Sprintf("abc%d", i+1), i+1)
		assert.NoError(t, err)
	}

	err := conn.Flush()
	assert.NoError(t, err)

	for i := 0; i < n; i++ {
		rep, err := conn.Receive()
		assert.NoError(t, err)
		t.Logf("result:%v, %v", rep, err)
	}

	// get
	for i := 0; i < n; i++ {
		err := conn.Send("GET", fmt.Sprintf("abc%d", i+1))
		assert.NoError(t, err)
	}

	err = conn.Flush()
	assert.NoError(t, err)

	for i := 0; i < n; i++ {
		rep, err := conn.Receive()
		assert.NoError(t, err)

		if ss, err := redis.String(rep, err); err != nil {
			t.Fatalf("result%d, invalid reply:%v, err:%s", i+1, rep, err)
		} else {
			t.Logf("result:%s, %v", ss, err)
		}
	}
}

func TestMultiCmd(t *testing.T) {
	cp := WithPool(t)
	conn := cp.Get()
	defer conn.Close()
	var args []interface{}
	n := 6
	for i := 0; i < n; i++ {
		args = append(args, fmt.Sprintf("mkey%d", i+1))
		args = append(args, fmt.Sprintf("%d", i+1))
	}
	rep, err := conn.Do("MSET", args...)
	assert.NoError(t, err)
	t.Logf("set result:%#v", rep)

	args = nil
	for i := 0; i < n; i++ {
		args = append(args, fmt.Sprintf("mkey%d", i+1))
	}
	rep, err = conn.Do("MGET", args...)
	assert.NoError(t, err)
	t.Logf("get result:%+v", rep)
}

func TestDoWithTimeout(t *testing.T) {
	cp := WithPool(t)
	conn := cp.Get()
	defer conn.Close()
	cwt, ok := conn.(redis.ConnWithTimeout)
	if !ok {
		t.Fatalf("not ConnWithTimeout interface")
		return
	}
	// must be timedout even though local redis server
	_, err := cwt.DoWithTimeout(time.Microsecond, "SET", "abc", "123")
	assert.Error(t, err, "seems timeout parameter didn't work")

	rep, err := cwt.DoWithTimeout(time.Millisecond*100, "SET", "abc", "123")
	assert.NoError(t, err, "DoWithTimeout error")
	t.Logf("get result:%s", rep)
}

func TestDoWithContext(t *testing.T) {
	cp := WithPool(t)
	conn := cp.Get()
	defer conn.Close()
	cwt, ok := conn.(redis.ConnWithContext)
	if !ok {
		t.Fatalf("not ConnWithTimeout interface")
		return
	}
	// must be timedout even though local redis server
	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()
	_, err := cwt.DoContext(ctx, "SET", "abc", "123")
	assert.Error(t, err, "seems ctx parameter didn't work")

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Millisecond*100)
	defer cancel2()
	rep, err := cwt.DoContext(ctx2, "SET", "abc", "123")
	assert.NoError(t, err, "DoContext error")
	t.Logf("get result:%s", rep)
}
