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
1. ClusterPool struct
   A struct that manages cluster connection pool. By calling its Getxx API, users can request redis.Conn interface that can be used to access Redis Cluster directly. It also manages underlying Conn corresponding to the nodes in the cluster by a pool list inside.

2. RedirConn struct
   A struct that implements redis.Conn interface and can handle redirecting automatically when MOVED or ASK occur.
   
3. Pipeliner
   A struct that implements redis.Conn interface and can handle redis pipeline commands directly. It scans all commands in the pipeline and then sorts out them into different sub-pipeline commands according to the slots. All sub-pipeline commands will be sent concurrently. If redirecting occurs to some commands, it will request them again to the right nodes indicated by the redirecting response. Once all of these sub-commands are returned, it composites them into a single response to the caller in the original command order.

## LICENSE
Redicluster is available under the [Apache License, Version 2.0](http://www.apache.org/licenses/LICENSE-2.0.html).