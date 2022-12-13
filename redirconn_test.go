package redicluster

import (
	"fmt"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
)

func TestPipeLineSet(t *testing.T) {
	cp := WithPool()
	cp.ReloadSlotMapping()
	conn := cp.Get()
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
