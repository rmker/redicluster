package redicluster

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
)

// redirconn is a struct that implements redis.Conn interface, which supports most of commands for redis cluster,
// including redirection handling automatically, multikeys command(MSET/MGET) and pipeline. Meanwhile, redirconn
// also works well if you are using stand-alone redis.

type redirconn struct {

	// cp is the pointer to the ClusterPool, immutable
	cp *ClusterPool

	// redir decides if the conn should handle redirecting, immutable
	redir bool

	// if this is a read only conn
	readOnly bool

	// protect the following members
	mu sync.Mutex

	// ppl is the pipeLiner (specified by Send() API)
	ppl *pipeLiner

	// cachedRc is the last redis.Conn used by Do() API
	lastRc redis.Conn

	// cachedAddr is the last node address used by Do() API
	lastAddr string
}

type RedirInfo struct {
	// Kind indicates the redirection type, MOVED or ASK
	Kind string

	// Slot is the slot number of the redirecting
	Slot int

	// Addr is the node address to redirect to
	Addr string

	// Raw is the original error string
	Raw string
}

// ParseRedirInfo parses the redirecting error into redirInfo
func ParseRedirInfo(err error) *RedirInfo {
	re, ok := err.(redis.Error)
	if !ok {
		return nil
	}
	parts := strings.Fields(re.Error())
	if len(parts) != 3 || (parts[0] != "MOVED" && parts[0] != "ASK") {
		return nil
	}
	slot, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}
	return &RedirInfo{
		Kind: parts[0],
		Slot: slot,
		Addr: parts[2],
		Raw:  re.Error(),
	}
}

func (c *redirconn) hookCmd(ctx context.Context, cmd string, args ...interface{}) (reply interface{}, err error, hooked bool) {
	switch cmd {
	case "MSET":
		rep, err := multiset(ctx, c, args...)
		return rep, err, true
	case "MGET":
		rep, err := multiget(ctx, c, args...)
		return rep, err, true
	default:
		return nil, nil, false
	}
}

func connDoContext(conn redis.Conn, ctx context.Context, cmd string, args ...interface{}) (interface{}, error) {
	if conn == nil {
		return nil, errors.New("invalid conn")
	}
	cwt, ok := conn.(redis.ConnWithContext)
	if ok {
		return cwt.DoContext(ctx, cmd, args...)
	} else {
		return conn.Do(cmd, args...)
	}
}

func connReceiveContext(conn redis.Conn, ctx context.Context) (interface{}, error) {
	if conn == nil {
		return nil, errors.New("invalid conn")
	}
	cwt, ok := conn.(redis.ConnWithContext)
	if ok {
		return cwt.ReceiveContext(ctx)
	} else {
		return conn.Receive()
	}
}

// Close closes the connection.
func (c *redirconn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ppl != nil {
		return c.ppl.Close()
	}
	if c.lastRc != nil {
		c.lastRc.Close()
		c.lastRc = nil
		c.lastAddr = ""
	}
	return nil
}

// Err returns a non-nil value when the connection is not usable.
func (c *redirconn) Err() error {
	return nil
}

// Do sends a command and return the reply with context.Background() by calling DoContext
func (c *redirconn) Do(cmd string, args ...interface{}) (reply interface{}, err error) {
	return c.DoContext(context.Background(), cmd, args...)
}

// DoWithTimeout sends a command and return the reply with timeout by calling DoContext
func (c *redirconn) DoWithTimeout(timeout time.Duration, cmd string, args ...interface{}) (reply interface{}, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.DoContext(ctx, cmd, args...)
}

// DoContext sends a command to the server and returns the received reply.
// Request will be sent to the node automatically that redirection error indicates if redir is true and redirecting occurs
func (c *redirconn) DoContext(ctx context.Context, cmd string, args ...interface{}) (reply interface{}, err error) {
	if repl, err, hooked := c.hookCmd(ctx, cmd, args...); hooked {
		return repl, err
	}
	addrs, err := c.cp.GetAddrsBySlots([]int{CmdSlot(cmd, args...)}, c.readOnly)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 || len(addrs[0]) == 0 {
		return nil, errors.New("empty node address")
	}

	addr := addrs[0]
	c.mu.Lock()
	if addr != c.lastAddr || c.lastRc == nil {
		conn, err := c.cp.getRedisConnByAddrContext(ctx, addr)
		if conn == nil {
			return nil, errors.New("invalid conn")
		}
		if err != nil {
			return nil, err
		}
		c.lastAddr = addr
		c.lastRc = conn
	}
	conn := c.lastRc
	c.mu.Unlock()

	repl, err1 := connDoContext(conn, ctx, cmd, args...)

	if err1 != nil {
		ri := ParseRedirInfo(err1)
		if ri != nil {
			c.cp.onRedir(ri)
			conn, err := c.cp.getRedisConnByAddrContext(ctx, ri.Addr)
			if conn != nil && err == nil {
				repl, err1 = connDoContext(conn, ctx, cmd, args...)
				c.mu.Lock()
				c.lastAddr = ri.Addr
				c.lastRc = conn
				c.mu.Unlock()
			}
		}
	}
	reply = repl
	err = err1
	return
}

// Send writes the command to the pipeLiner
func (c *redirconn) Send(cmd string, args ...interface{}) error {
	c.mu.Lock()
	if c.ppl == nil {
		c.ppl = newPipeliner(c.cp)
	}
	c.mu.Unlock()
	return c.ppl.send(cmd, args...)
}

// Flush flushes the output buffer to the Redis server
func (c *redirconn) Flush() error {
	c.mu.Lock()
	if c.ppl == nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.ppl.flush(context.Background())
}

// Receive receives a single reply from the pipeLiner.
// Note: this func is a non-block operation since pipeLiner just copy the reply to the caller from received buffer
func (c *redirconn) Receive() (reply interface{}, err error) {
	c.mu.Lock()
	if c.ppl == nil {
		c.mu.Unlock()
		return nil, errors.New("no send request before")
	}
	c.mu.Unlock()
	return c.ppl.receive()
}

// Receive receives a single reply from the pipeLiner with timeout.
// Note: just for implementing ConnWithTimeout, timeout will be ignored since there is no I/O operation in pipeLiner
func (c *redirconn) ReceiveWithTimeout(timeout time.Duration) (reply interface{}, err error) {
	c.mu.Lock()
	if c.ppl == nil {
		c.mu.Unlock()
		return nil, errors.New("no send request before")
	}
	c.mu.Unlock()
	return c.ppl.receive()
}

// Receive receives a single reply from the pipeLiner with context.
// Note: just for implementing ConnWithContext, ctx will be ignored since there is no I/O operation in pipeLiner
func (c *redirconn) ReceiveContext(ctx context.Context) (reply interface{}, err error) {
	c.mu.Lock()
	if c.ppl == nil {
		c.mu.Unlock()
		return nil, errors.New("no send request before")
	}
	c.mu.Unlock()
	return c.ppl.receive()
}
