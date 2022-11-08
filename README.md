# redicluster
redicluster is a golang client driver for redis cluster that is built on top of [gomudle/redigo](https://github.com/gomodule/redigo).
It aimes to support pipeline and multi-keys commands to access redis cluster without proxy, besides of most of redis commands.

Main features:
1. Single key API
2. Multi-keys API: MGET/MSET
3. Pipeline
4. Lua scripts
5. Auto-Redirecting

## LICENSE
redicluster is available under the [Apache License, Version 2.0](http://www.apache.org/licenses/LICENSE-2.0.html).