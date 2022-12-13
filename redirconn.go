package redicluster

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gomodule/redigo/redis"
)

type redirconn struct {

	// cp is the pointer to the ClusterPool, immutable
	cp *ClusterPool

	// redir decides if the conn should handle redirecting
	redir bool

	// boundConn is the connection bound with an addr (specified by Send API)
	boundConn redis.Conn
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

// Close closes the connection.
func (c *redirconn) Close() error {
	if c.boundConn != nil {
		return c.boundConn.Close()
	}
	return nil
}

// Err returns a non-nil value when the connection is not usable.
func (c *redirconn) Err() error {
	if c.boundConn != nil {
		return c.boundConn.Err()
	}
	return nil
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

// Do sends a command to the server and returns the received reply.
// Request will be sent to the node automatically that redirection error indicates if redir is true and redirecting occurs
func (c *redirconn) Do(cmd string, args ...interface{}) (reply interface{}, err error) {
	conn, err := c.cp.getRedisConnBySlot(CmdSlot(cmd, args...))
	// conn, err := c.cp.getRedisConn()
	if conn == nil {
		return nil, errors.New("invalid conn")
	}
	repl, err1 := conn.Do(cmd, args...)
	conn.Close()
	if err1 != nil {
		ri := ParseRedirInfo(err1)
		if ri != nil {
			c.cp.onRedir(ri)
			conn, err = c.cp.getRedisConnByAddr(ri.Addr)
			if conn != nil {
				repl, err1 = conn.Do(cmd, args...)
				conn.Close()
			}
		}
	}
	reply = repl
	err = err1
	return
}

// Send writes the command to the client's output buffer.
func (c *redirconn) Send(cmd string, args ...interface{}) error {
	if c.boundConn == nil {
		conn, err := c.cp.getRedisConnBySlot(CmdSlot(cmd, args...))
		if err != nil {
			return err
		}
		if conn == nil {
			return errors.New("nil conn")
		}
		c.boundConn = conn
	}
	return c.boundConn.Send(cmd, args...)
}

// Flush flushes the output buffer to the Redis server.
func (c *redirconn) Flush() error {
	if c.boundConn != nil {
		return c.boundConn.Flush()
	}
	return nil
}

// Receive receives a single reply from the Redis server
func (c *redirconn) Receive() (reply interface{}, err error) {
	if c.boundConn != nil {
		return c.boundConn.Receive()
	}
	return nil, nil
}

func (c *redirconn) GetBoundConn() redis.Conn {
	return c.boundConn
}
