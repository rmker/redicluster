# Redicluster
Redicluster is a lightweight golang client driver for redis cluster and built on top of [gomudle/redigo](https://github.com/gomodule/redigo).
It aimes to support pipeline and multi-keys commands to access redis cluster without proxy, besides of most of redis commands.

## Project Background
Redis Cluster is complicated for users on the client side, compared to the standalone mode. Users must care about slot distribution and handle redirecting. There are a few libraries to help with this, but most of them can not support all the features well, like pipeline and scripting. The Redis proxy(like twitter/twemproxy and CodisLabs/codis) is a good way to solve this problem. Unfortunately, not all cloud providers support this kind of proxy.

## Project Goal
Redicluster aims to support all Redis Cluster features on the client side in a simple way. By using this library, users don't need to care about the clustering complexity(like slots distribution, redirecting handling, etc.), just access Redis Cluster like a standalone one or the cluster behind proxy.

Redicluster is implemented based on gomodule/redigo which is a lightweight but powerful redis client driver. You will access Redis through Redicluster just like using Redigo directly. Especially, you only need to make trivial changes to migrate standalone Redis to cluster one if you are using Redigo in your project.

## Main Features
### 1. Pool and connection management
The pool and connection interface defined in Redigo is exposed with drop-in replacement.

### 2. Slots mapping and routing
The slots mapping is stored in the pool object. It would be refreshed automatically once redirecting occurs every time, or updated manually by callers.

### 3. Redirecting handling
The Conn can request the right node indicated by the MOVED response automatically on the underlayer after redirecting occurs. The callers don't need to handle it on the application layer. Optionally, the callers can close this mechanism and handle redirecting by themselves.

### 4. Pipeline
A pipeline request always contains multiple keys. Unlike standalone Redis, those keys are highly probably located on different nodes in the Redis Cluster. We need to extract the keys and map them to the right nodes, and send multiple sub-requests to those nodes concurrently. Once all sub responses arrived, a final response composed by them in the original order will be returned to the caller. Obviously, the redirecting of every sub -request can be handled automatically, the same as mentioned above.

### 5. Multiple keys commands
On the underlayer, Multiple key commands(MGET, MSET, etc) will be transfered to a pipeline request according to the slot of the keys. All keys located on the same node will be put into the same command in the pipeline.

NOTE: Unlike standalone Redis, the atomicity of multiple key commands can't be guaranteed in the Redis Cluster, since those keys probably locate on different nodes and the request is processed concurrently. If the automatic handling of these commands is not expected by the caller, they have to handle the commands by themselves. So, Redicluster needs to guarantee flexibility for the caller.

### 6. Lua script
Lua script execution is an atomic operation in Redis. You are not able to process multiple keys that locate on different nodes in a cluster. So, we assume that all keys in Lua script have the same slot. For simplicity, the request will be sent to the node the first key indicates. For the caller, using hashtag is a feasible way to guarantee all keys locating on the same node.

### 7. Pub/Sub
A Pub/Sub message is propagated across the cluster to all subscribers. Any node can receive the message, so we don't need to do anything about it. But from Redis 7.0, [sharded Pub/Sub](https://redis.io/docs/manual/pubsub/#sharded-pubsub) channel is introduced to support sharded messages based on the channel key slots. Relevant commands(SSUBSCRIBE, SUNSUBSCRIBE, SPUBLISH, etc.) will be sent to the right node based on the channel key.

## Hierarchy
1. ClusterPool: the struct that manages cluster connection pool. By calling its Getxx API, users can request redis.Conn interface that can be used to access Redis Cluster directly. It also manages underlying Conn corresponding to the nodes in the cluster by a pool list inside.

2. pipeLiner: the struct that implements redis.Conn interface and can handle redis pipeline commands directly. It scans all commands in the pipeline and then sorts out them into different sub-pipeline commands according to the slots. All sub-pipeline commands will be sent concurrently. If redirecting occurs to some commands, it will request them again to the right nodes indicated by the redirecting response. Once all of these sub-commands are returned, it composites them into a single response to the caller in the original command order.

3. redirconn: the struct that implements redis.Conn interface, handles redirecting automatically when MOVED or ASK occur and passes send/receive/flush to a underlying pipeLiner to support pipeline for redis cluster. ClusterPool.Get() returns it's pointer to the caller.

## How to use
1. Creating a ClusterPool for your project
2. Invoking the ClusterPool.Get() to get a redis.Conn for access the cluster
3. Invoking the Close() of redis.Conn to return it to the ClusterPool if all operations done

A simple example:
```go
// create a cluster pool
func CreateClusterPool() *ClusterPool {
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

   // reload slot mapping in advance
   cp.ReloadSlotMapping()
   return cp
}

func printPubSubReceive(prefix string, r interface{}) {
	if v, ok := r.(redis.Subscription); ok {
		fmt.Printf("%s redis.Subscription: %v\n", prefix, v)
	} else if v, ok := r.(redis.Message); ok {
		fmt.Printf("%s redis.Message: %v\n", prefix, v)
	} else if v, ok := r.(redis.Pong); ok {
		fmt.Printf("%s redis.Pong: %v\n", prefix, v)
   } else {
      fmt.Printf("%s unknown value: %v\n", prefix, v)
   }
}

cp := CreateClusterPool()
conn := cp.Get()
defer conn.Close()

// Set and GET
rep, err := conn.Do("SET", "abc", "123")
fmt.Printf("SET result:%s, err=%s", rep, err)

rep, err = conn.Do("GET", "abc")
fmt.Printf("GET result:%s, err=%s", rep, err)

// MSET
rep, err := conn.Do("MSET", "abc", "123", "efg", "456")
fmt.Printf("MSET result:%s, err=%s", rep, err)

// pipeline
conn.Send("GET", "abc")
conn.Send("GET", "efg")
conn.Flush()

rep, err = conn.Receive()
fmt.Printf("pipeline result1:%s, err=%s", rep, err)

rep, err = conn.Receive()
fmt.Printf("pipeline result2:%s, err=%s", rep, err)

// PubSub
s1, err := cp.GetPubSubConn()
s1.Subscribe("ChnA")
printPubSubReceive("s1.Subscribe", s1.Receive())

s2, err := cp.GetPubSubConn()
s2.Subscribe("ChnA")
printPubSubReceive("s2.Subscribe", s2.Receive())

conn.Do("SPUBLISH", "ChnA", "I'm msg")

printPubSubReceive("push to s1", s1.Receive())
printPubSubReceive("push to s2", s2.Receive())

// ShardedPubSub
ss1, err := cp.GetShardedPubSubConn()
ss1.SSubscribe("ShardedChnA")
printPubSubReceive("ss1.Subscribe", ss1.Receive())

ss2, err := cp.GetShardedPubSubConn()
ss2.SSubscribe("ShardedChnA")
printPubSubReceive("ss2.Subscribe", ss2.Receive())

conn.Do("SPUBLISH", "ShardedChnA", "I'm sharded msg")

printPubSubReceive("push to ss1", ss1.Receive())
printPubSubReceive("push to ss2", ss2.Receive())
```
## Reference
1. Redis Cluster Spec: https://redis.io/docs/reference/cluster-spec/
2. Redigo: https://github.com/gomodule/redigo
3. Redisc: https://github.com/mna/redisc

## LICENSE
Redicluster is available under the [Apache License, Version 2.0](http://www.apache.org/licenses/LICENSE-2.0.html).