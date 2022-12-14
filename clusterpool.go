package redicluster

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
)

// ClusterPool is a redis cluster manager and nodes pool, that handles the slot mapping and the pool of connections to cluster nodes

const (
	TotalSlots = 16384
)

type nodeInfo struct {
	Addr string
	Id   string
}

type slotInfo struct {
	Start, End int
	Nodes      []*nodeInfo
	Addrs      []string
}

type ClusterPool struct {
	// The entry addresses for cluster, which can be any node address in cluster
	EntryAddrs []string

	// Dial options for case without pool(CreateConnPool is nil)
	DialOptionsWithoutPool []redis.DialOption

	// Defualt timeout for the connection pool
	DefaultPoolTimeout time.Duration

	// Function for creating connection pool, which would be invoked when the caller acquires conn by Getxx func
	// if the node has not pool in connPools. By this func, you can control the pool behavior based on your demand
	CreateConnPool func(ctx context.Context, addr string) (*redis.Pool, error)

	// protect the following members
	mu sync.Mutex

	// slot mapping, slot -> addrs, the 0 index address is the master
	slotAddrMap [TotalSlots][]string

	// slot info, including slot range and corresponding nodes address and roles
	slots []*slotInfo

	// connections pool for nodes in cluster
	connPools map[string]*redis.Pool

	// reloading indicates that the slot mapping is reloading
	reloading bool
}

// Slot returns the hash Slot of the key
func Slot(key string) int {
	if start := strings.Index(key, "{"); start >= 0 {
		if end := strings.Index(key[start+1:], "}"); end > 0 {
			end += start + 1
			key = key[start+1 : end]
		}
	}
	return int(crc16(key) % TotalSlots)
}

// CmdSlot returns the hash slot of the command
func CmdSlot(cmd string, args ...interface{}) int {
	// -1 when args is nil, a random slot should be taken for invoker like GetAddrsBySlots
	slot := -1
	sk := 0

	// for script command, use the first key to calculate slot, so we can do a script with only one key handling
	// NOTE: you should not handle muliple keys in one script command in a redis cluster
	switch cmd {
	case "EVAL", "EVAL_RO", "EVALSHA", "EVALSHA_RO":
		sk = 2
	}
	if len(args) > sk {
		key := fmt.Sprintf("%s", args[sk])
		slot = Slot(key)
	}
	return slot
}

/* redis.Pool compatible APIs */

// Get gets the redis.Conn interface that handles the redirecting automatically
func (cp *ClusterPool) Get() redis.Conn {
	return &redirconn{cp: cp, redir: true, readOnly: false}
}

// GetContext gets the redis.Conn interface that handles the redirecting automatically
func (cp *ClusterPool) GetContext(ctx context.Context) redis.Conn {
	return &redirconn{cp: cp, redir: true, readOnly: false}
}

// Stats gets the redis.PoolStats of the current cluster
func (cp *ClusterPool) Stats() map[string]redis.PoolStats {
	ps := make(map[string]redis.PoolStats)
	cp.mu.Lock()
	for k, p := range cp.connPools {
		ps[k] = p.Stats()
	}
	cp.mu.Unlock()
	return ps
}

// Close closes the connections and clear the slot mapping of the cluster pool
func (cp *ClusterPool) Close() {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for k, p := range cp.connPools {
		p.Close()
		delete(cp.connPools, k)
	}
	cp.slots = nil
	for i := range cp.slotAddrMap {
		cp.slotAddrMap[i] = nil
	}
}

// ActiveCount returns the total active connection count in the cluster pool
func (cp *ClusterPool) ActiveCount() int {
	n := 0
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for _, p := range cp.connPools {
		n += p.ActiveCount()
	}
	return n
}

// IdleCount returns the total idle connection count in the cluster pool
func (cp *ClusterPool) IdleCount() int {
	n := 0
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for _, p := range cp.connPools {
		n += p.IdleCount()
	}
	return n
}

/* redis.Pool compatible APIs end */

func (cp *ClusterPool) GetRandomRealConn() (redis.Conn, error) {
	return cp.getRedisConnBySlot(-1)
}

// GetNoRedirConn gets the redis.Conn interface without redirecting handling, which allows
// you handling the redirecting
func (cp *ClusterPool) GetNoRedirConn() redis.Conn {
	return &redirconn{cp: cp, redir: false, readOnly: false}
}

// GetConnWithoutRedir gets the redis.Conn interface without redirecting handling, which allows
// you handling the redirecting
func (cp *ClusterPool) GetReadonlyConn() redis.Conn {
	return &redirconn{cp: cp, redir: true, readOnly: true}
}

// GetPubSubConn gets the redis.PubSubConn
func (cp *ClusterPool) GetPubSubConn() (*redis.PubSubConn, error) {
	conn, err := cp.GetRandomRealConn()
	if err != nil {
		return nil, err
	}
	return &redis.PubSubConn{
		Conn: conn,
	}, nil
}

// GetShardedPubSubConn gets the ShardedPubSubConn
func (cp *ClusterPool) GetShardedPubSubConn() (*ShardedPubSubConn, error) {
	return &ShardedPubSubConn{
		cp: cp,
	}, nil
}

// VerbosSlots returns the slot mapping of the cluster with a readable string
func (cp *ClusterPool) VerbosSlotMapping() string {
	var s []string
	for i, si := range cp.slots {
		s = append(s, fmt.Sprintf("%d) Slot Range: %d - %d", i+1, si.Start, si.End))
		for j, ni := range si.Nodes {
			role := ""
			if j == 0 {
				role = "(master)"
			}
			s = append(s, fmt.Sprintf("   Node %d: %s, %s%s", j+1, ni.Addr, ni.Id, role))
		}
	}
	return strings.Join(s, "\n")
}

// ReloadSlots reloads the slot mapping
func (cp *ClusterPool) ReloadSlotMapping() error {
	return cp.reloadSlotMaping()
}

// a *rand.Rand is not safe for concurrent access
var rnd = struct {
	sync.Mutex
	*rand.Rand
}{Rand: rand.New(rand.NewSource(time.Now().UnixNano()))} //nolint:gosec

func (cp *ClusterPool) GetAddrsBySlots(slots []int, readOnly bool) ([]string, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	var addrs []string
	for _, sl := range slots {
		if sl >= TotalSlots {
			return nil, errors.New("invalid slot")
		} else if sl < 0 {
			rnd.Lock()
			sl = rnd.Intn(TotalSlots)
			rnd.Unlock()
		}
		sa := cp.slotAddrMap[sl]
		if len(sa) == 0 {
			return nil, errors.New("bad slot mapping")
		}
		addr := sa[0]
		if readOnly {
			if len(sa) == 2 {
				addr = sa[1]
			} else {
				rnd.Lock()
				ix := rnd.Intn(len(sa) - 1)
				rnd.Unlock()
				addr = addrs[ix+1]
			}
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// onRedir triggers the reloading
func (cp *ClusterPool) onRedir(ri *RedirInfo) bool {
	doReload := false

	// Reload only the kind is MOVED. ASK redirecting indicates that only the next query need to redirect,
	// so we don't need to reload the slot mapping
	// ASK spec: https://redis.io/docs/reference/cluster-spec/#ask-redirection
	if ri != nil && ri.Kind == "MOVED" {
		if ri.Slot < TotalSlots {
			curAddr := cp.slotAddrMap[ri.Slot]

			// Reload only if ri.Addr is not equal to the corresponding addr in the slot mapping.
			// MOVED occurs and redirects to the master as a request is sent to a replica, so we don't need to
			// reload the slot mapping if the ri.Addr is same as the addr in the slot mapping
			if len(curAddr) == 0 || curAddr[0] != ri.Addr {
				cp.slotAddrMap[ri.Slot] = []string{ri.Addr}
				doReload = true
			}
		}
	}
	if doReload {
		// reload concurrently for future requests, and the trigger routine should try again with the addr in the RedirInfo
		go cp.reloadSlotMaping()
	}
	return doReload
}

func (cp *ClusterPool) getRedisConnByAddr(addr string) (redis.Conn, error) {
	if cp.DefaultPoolTimeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), cp.DefaultPoolTimeout)
		defer cancel()
		return cp.getRedisConnByAddrContext(ctx, addr)
	}
	return cp.getRedisConnByAddrContext(context.Background(), addr)
}

func (cp *ClusterPool) getRedisConnByAddrContext(ctx context.Context, addr string) (redis.Conn, error) {
	var (
		np  *redis.Pool
		err error
	)
	if len(addr) == 0 {
		return nil, errors.New("invalid addr")
	}
	cp.mu.Lock()
	if cp.connPools == nil {
		cp.connPools = make(map[string]*redis.Pool)
	}
	if cp.connPools[addr] == nil {
		if cp.CreateConnPool == nil {
			cp.mu.Unlock()
			return cp.defaultDial(ctx, addr)
		}
		np, err = cp.CreateConnPool(ctx, addr)
		if err != nil {
			cp.mu.Unlock()
			return nil, err
		}
		cp.connPools[addr] = np
	} else {
		np = cp.connPools[addr]
	}
	cp.mu.Unlock()
	return np.GetContext(ctx)
}

func (cp *ClusterPool) getRedisConnBySlot(slot int) (redis.Conn, error) {
	if slot >= TotalSlots {
		return nil, errors.New("invalid slot")
	}
	if slot < 0 {
		rnd.Lock()
		slot = rnd.Intn(TotalSlots)
		rnd.Unlock()
	}
	cp.mu.Lock()
	if len(cp.slotAddrMap[slot]) > 0 {
		addr := cp.slotAddrMap[slot][0]
		cp.mu.Unlock()
		return cp.getRedisConnByAddr(addr)
	}
	cp.mu.Unlock()
	cp.reloadSlotMaping()

	cp.mu.Lock()
	if len(cp.slotAddrMap[slot]) == 0 {
		cp.mu.Unlock()
		return nil, errors.New("no slots")
	}
	addr := cp.slotAddrMap[slot][0]
	cp.mu.Unlock()
	return cp.getRedisConnByAddr(addr)
}

func (cp *ClusterPool) reloadSlotMaping() error {
	cp.mu.Lock()
	if cp.reloading {
		return nil
	}
	cp.reloading = true
	cp.mu.Unlock()
	defer func() {
		cp.mu.Lock()
		cp.reloading = false
		cp.mu.Unlock()
	}()

	nodes := cp.getNodes(true)
	if len(nodes) == 0 {
		return errors.New("empty node")
	}
	for _, addr := range nodes {
		conn, err := cp.getRedisConnByAddr(addr)
		if err != nil || conn == nil {
			continue
		}
		rep, err := conn.Do("CLUSTER", "SLOTS")
		conn.Close()
		if err != nil {
			continue
		}
		if cp.updateSlotMap(rep) == nil {
			return nil
		}
	}
	return errors.New("all nodes failed")
}

func (cp *ClusterPool) updateSlotMap(rep interface{}) error {
	slots, err := redis.Values(rep, nil)
	if err != nil {
		return err
	}

	var sis []*slotInfo
	for _, sl := range slots {
		psi := &slotInfo{}
		si, err := redis.Values(sl, nil)
		if err != nil {
			return err
		}
		nis, err := redis.Scan(si, &psi.Start, &psi.End)
		if err != nil {
			return err
		}
		for _, ni := range nis {
			var a, id string
			var p int
			fs, err := redis.Values(ni, nil)
			if err != nil {
				return err
			}
			_, err = redis.Scan(fs, &a, &p, &id)
			if err != nil {
				return err
			}
			addr := fmt.Sprintf("%s:%d", a, p)
			psi.Nodes = append(psi.Nodes, &nodeInfo{
				Addr: addr,
				Id:   id,
			})
			psi.Addrs = append(psi.Addrs, addr)
		}
		sis = append(sis, psi)
	}
	cp.mu.Lock()
	cp.slots = sis
	for _, si := range sis {
		for i := si.Start; i <= si.End; i++ {
			cp.slotAddrMap[i] = si.Addrs
		}
	}
	cp.mu.Unlock()
	return nil
}

func (cp *ClusterPool) defaultDial(ctx context.Context, addr string) (redis.Conn, error) {
	return redis.Dial("tcp", addr, cp.DialOptionsWithoutPool...)
}

func (cp *ClusterPool) getNodes(replica bool) []string {
	var nodes []string
	cp.mu.Lock()
	defer cp.mu.Unlock()
	for _, sl := range cp.slots {
		for i, n := range sl.Nodes {
			if i > 0 && !replica {
				continue
			}
			nodes = append(nodes, n.Addr)
		}
	}
	if len(nodes) == 0 {
		nodes = append(nodes, cp.EntryAddrs...)
	}
	return nodes
}
