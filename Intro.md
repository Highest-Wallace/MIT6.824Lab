### Situation (背景)

麻省理工大学课程项目，该项目旨在通过实践，深入理解和掌握分布式系统的核心概念和技术。项目要求基于提供的Go语言代码框架，参考经典论文，从零开始逐步构建一个功能完整、高度容错、支持分片的分布式存储系统。这是一个极具挑战性的项目，对系统设计的健壮性和正确性有非常高的要求。

### Task (任务)

您的核心任务是实现一个多阶段、功能逐步增强的分布式系统，主要包含以下四个关键组件：

1. **实现MapReduce并行计算框架**：构建一个能够并行处理大规模数据集的计算框架，包括一个中心化的Coordinator节点负责任务调度和故障恢复，以及多个Worker节点负责执行实际的Map和Reduce计算任务 。
2. **实现Raft一致性协议**：复刻Raft论文中的细节，实现一个完整的Raft共识算法库 。这包括领导者选举、日志复制与分发、状态持久化以及日志压缩（通过快照）等核心功能，为上层应用提供强一致性保障。
3. **实现容错的键值存储服务 (Fault-tolerant Key/Value Service)**：在Raft协议的基础上，构建一个高可用的键值数据库服务 。该服务集群通过Raft保证操作的线性一致性，客户端可以对存储在内存中的键值对进行读、写和追加操作 。
4. **实现分片的键值存储服务 (Sharded Key/Value Service)**：将单一的键值数据库扩展为支持数据分片的分布式系统 。这需要实现一个Shard Controller服务来管理分片配置，各个分片服务组 (Service Group) 内部使用Raft保证容错，并能根据配置在不同Group之间迁移分片 (Shard)，以实现数据负载均衡，减轻单一节点的存储压力，提高系统的整体响应效率 。

最终目标是通过所有预设的测试用例，这些测试模拟了包括网络不可靠、服务器崩溃、客户端重启以及RPC调用次数限制等复杂场景，并且要求稳定通过超过5000次。

### Action (行动)

您在项目中采取了以下关键行动，并应用了相应的技术知识：

**1. MapReduce 并行计算框架 (`src/mr/coordinator.go`, `src/mr/worker.go`)**

- 任务调度与管理：
  - 在 `mr/coordinator.go` 中，您设计并实现了`Coordinator`结构体来管理Map任务和Reduce任务的生命周期。任务状态（如 `WAITTING`, `STARTED`, `FINISHED`）被明确定义和追踪。
  - `Coordinator`通过RPC (`WorkspaceTask`) 向 `Worker` 分配任务。它会优先分配Map任务，待所有Map任务完成后再分配Reduce任务。
  - 使用了 `sync.Mutex` 进行并发控制，保护共享的任务状态数据，并结合 `sync.Cond` 实现 `Worker` 在无任务可分配时的等待与唤醒机制。
- 故障处理：
  - `Coordinator` 为每个已分配但未完成的任务启动一个超时计时器（例如10秒）。如果在规定时间内 `Worker` 未能完成任务（可能由于崩溃或网络问题），`Coordinator`会将该任务状态重置为 `WAITTING`，以便重新分配给其他 `Worker`。这体现在 `mapTaskStarted` 和 `reduceTaskStarted` 函数内部的goroutine中。
- Worker 实现：
  - 在 `mr/worker.go` 中，`Worker` 循环向 `Coordinator` 请求任务。
  - 收到Map任务后，`Worker` 读取输入文件，调用用户定义的 `mapf` 函数，并将中间结果通过 `ihash` 函数分区后写入多个中间文件 (mr-X-Y)。文件的写入采用了先写临时文件再原子重命名的策略，以防止部分写入问题。
  - 收到Reduce任务后，`Worker` 读取对应的所有中间文件，对键值对按键排序，然后调用用户定义的 `reducef` 函数，并将最终结果写入输出文件 (mr-out-X)。
- **RPC通信**：`Worker` 和 `Coordinator` 之间通过Go语言的 `net/rpc` 包进行通信，定义了如 `WorkspaceTaskArgs`, `WorkspaceTaskReply`, `TaskFinishedArgs` 等RPC消息结构。

**所用知识点**：Go语言并发编程（goroutines, channels, mutex, condition variables）、RPC、分布式任务调度、故障检测与恢复机制、文件原子操作。

**2. Raft 一致性协议 (`src/raft/raft.go`)**

- 领导者选举：
  - 实现了Raft论文图2中描述的服务器状态 (Follower, Candidate, Leader) 转换逻辑。
  - Follower在选举超时后转为Candidate，增加 `currentTerm`，投票给自己，并向其他peers发送 `RequestVote` RPC 。
  - Candidate收到多数选票后成为Leader 。Leader会周期性地向所有Followers发送心跳 (空的 `AppendEntries` RPC) 来维持其领导地位并阻止新选举 。
  - 选举超时时间被随机化以减少选举冲突 。
- 日志复制与分发：
  - Leader接收客户端请求 (通过 `Start()` 方法)，将命令作为日志条目 (Entry) 追加到自己的日志中 。
  - Leader通过 `AppendEntries` RPC将新的日志条目复制给Followers。`AppendEntries` RPC包含了 `prevLogIndex` 和 `prevLogTerm` 用于保证日志的一致性检查。
  - Follower收到 `AppendEntries` RPC后，会进行一致性检查，若通过则追加日志条目，并返回成功；若不成功会返回冲突任期的最早索引。
  - Leader维护了 `nextIndex` 和 `matchIndex` 数组来追踪每个Follower的日志复制进度。
  - 当Leader发现Follower的日志与其不一致时，会递减 `nextIndex` 并重试 `AppendEntries`，直到找到匹配点，然后发送后续的日志条目，实现了快速日志回溯的优化 。
- 持久化存储：
  - `currentTerm`, `votedFor`, 和 `log` (日志条目) 被设计为持久化状态 。
  - 每当这些状态发生变化时（例如，`votedFor` 更新、新日志条目追加），都会调用 `persist()` 方法，将状态序列化后通过 `Persister` 对象保存 。这确保了节点崩溃重启后能恢复之前的状态。
- 状态机快照 (日志压缩)：
  - 实现了 `Snapshot(index int, snapshot []byte)` 方法，允许上层服务通知Raft某个 `index` 之前的日志已经被包含在 `snapshot` 中 。
  - Raft会丢弃该 `index` 之前的日志条目，并将快照元数据 (如 `snapshotIndex`, `snapshotTerm`) 和快照本身持久化 。
  - 当Leader需要发送给某个Follower的日志条目已经被快照丢弃时，Leader会通过 `InstallSnapshot` RPC将整个快照发送给Follower 。Follower接收到快照后，会应用它并更新自己的状态 。
- RPC通信与并发控制：
  - Raft节点间的通信完全基于RPC（`labrpc` 包） 。
  - 大量使用了 `sync.RWMutex` 来保护Raft内部共享状态的并发访问。
  - 通过 `applyCh` 通道将已提交的日志条目异步地发送给上层应用状态机执行 。

**所用知识点**：Go语言并发与RPC、分布式共识算法 (Raft)、状态机复制、持久化存储、日志压缩与快照、网络分区与节点故障处理。

**3. 容错的键值存储服务 (kvraft) (`src/kvraft/server.go`)**

- 基于Raft的线性一致性：
  - 每个kvserver实例内部包含一个Raft peer 。客户端的Put/Append/Get操作请求首先被提交给Raft集群的Leader。
  - `KVServer`的 `PutAppend` 和 `Get` RPC处理器会将操作 (Op) 封装后调用其Raft实例的 `Start()` 方法，将操作提议到Raft日志中 。
  - 所有kvserver实例从Raft的 `applyCh` 中接收已提交的日志条目 (Op)，并按顺序应用到本地的键值存储 (一个内存 `map[string]string`) 。由于所有副本以相同顺序应用相同操作，从而保证了数据的一致性。
- 客户端请求处理与重试：
  - `Clerk` (客户端) 负责与kvserver集群交互。它会追踪当前的Leader，并优先向Leader发送请求 。如果请求失败 (例如，Leader改变或网络问题)，`Clerk`会重试其他服务器，直到找到新的Leader 。
- 重复请求处理 (At-Most-Once Semantics)：
  - 为防止因客户端重试导致同一操作被执行多次，引入了客户端ID (`ClientId`) 和序列号 (`SeqNum`) 机制 。
  - `KVServer` 会为每个客户端记录其已处理的最新 `SeqNum` (存储在 `clientsStatus` 结构中)。
  - 在应用操作之前，`KVServer`会检查该操作的 `ClientId` 和 `SeqNum`。如果 `SeqNum` 小于或等于已记录的该客户端的 `lastSeqNum`，则说明该操作已被处理过，直接返回之前的结果或忽略，从而保证每个操作至多执行一次。
- 快照集成：
  - 当Raft日志增长到一定大小时 (`maxraftstate`)，`KVServer`会创建其当前键值存储和客户端 `SeqNum` 状态的快照，并调用Raft的 `Snapshot()` 方法，以便Raft可以压缩其日志 。
  - `KVServer` 实现了 `applySnap` 方法，用于在Raft层通知应用快照时，从快照数据中恢复键值存储和客户端状态。

**所用知识点**：Go语言、Raft协议应用、线性一致性、RPC、客户端请求路由与重试、幂等性保证 (at-most-once semantics)、分布式系统快照机制。

**4. 分片的键值存储服务 (shardkv) (`src/shardkv/server.go`, `src/shardctrler/server.go`)**

- Shard Controller (`shardctrler/server.go`)：
  - 实现了一个中心化的、容错的`ShardCtrler`服务，其本身也由Raft保证一致性 。
  - `ShardCtrler` 负责管理一系列配置 (Configuration)。每个配置定义了哪些分片 (Shards) 由哪些副本组 (GID) 负责 。
  - 提供了 `Join` (添加新副本组并重新平衡分片)、`Leave` (移除副本组并重新分配其分片)、`Move` (移动特定分片到特定组) 和 `Query` (查询特定编号或最新的配置) 等RPC接口 。
  - 配置变更时，`ShardCtrler` 会创建新的配置，并尽可能均匀地分配分片，同时最小化分片的移动次数 。
  - 同样实现了客户端请求的重复检测机制 。
- ShardKV Server (`shardkv/server.go`)：
  - 每个 `ShardKV` 服务器属于一个副本组 (GID)，每个组内部使用独立的Raft实例进行容错 。
  - `ShardKV` 服务器定期从 `ShardCtrler` 拉取最新的配置信息 (`pollConfig` 方法) 。
  - **请求路由与处理**：客户端请求会根据键 (`key2shard(key)`) 确定其所属分片，然后根据当前配置找到负责该分片的副本组，并将请求发送给该组的Leader。如果请求被发送到错误的组 (例如由于配置变更)，服务器会返回 `ErrWrongGroup` 错误，客户端会查询最新配置并重试 。
  - 分片迁移 (Configuration Changes)：
    - 当检测到配置变更时 (`changeConfig` 方法)，服务器需要进行分片迁移 。
    - 如果一个组在新配置中获得了某些分片，它需要从旧的负责组那里拉取这些分片的数据 (`getMigrationFromGroupShardsMap`, `callGetShardsFromGroup`)。
    - 一旦收到分片数据和相关的客户端状态 (如 `lastSeqNum`，确保at-most-once语义跨迁移)，就将这些数据应用到自己的状态中 (`PutShards` RPC, `applyOp` 中处理 `PUT_SHARDS` 的逻辑)。
    - 如果一个组在新配置中失去了某些分片，它必须停止为这些分片的键提供服务，并将数据迁移给新的负责组。迁移完成后，可能会删除本地不再负责的分片数据 (通过 `DeleteShards` RPC) 。
    - 迁移过程本身也作为操作提交到Raft日志中，以确保组内所有副本在同一点进行配置转换和数据更新，保证一致性 。例如，`SetConfigArgs` 被包装成 `Op` 并通过Raft提交。
  - **RPC通信**：`ShardKV` 服务器之间需要RPC通信以传输分片数据 (`callOtherGroup` 方法封装了向其他组发送RPC的逻辑) 。
  - **快照与重复请求处理**：与`kvraft`类似，`shardkv` 也实现了基于Raft的快照机制来压缩日志，并维护客户端请求序列号以实现at-most-once语义，这些状态也需要在分片迁移时正确传递和恢复。

**所用知识点**：Go语言、Raft协议、数据分片 (Sharding)、配置管理、分布式事务 (分片迁移过程中的一致性保证)、RPC、负载均衡、动态扩缩容基础。

### Result (结果)

通过上述行动和技术应用，您成功地在提供的Go语言代码框架上，复刻了论文细节，实现了一个完整、高度容错、支持分片的分布式存储系统。该系统能够稳定通过所有测试样例超过5000次，这些测试覆盖了不可靠网络环境、服务器崩溃、客户端重启以及RPC次数限制等各种严苛的分布式系统故障场景。这充分证明了您实现的系统的正确性、健壮性和高性能。

## 项目遇到的困难

### Raft

1. 候选者发起选举后回收选票，当时我直接检查得票是否过半来判断是否成为leader。问题出现是没有对回收选票的任期进行检查，假设候选者A在任期3发起选举，向其他节点发送请求选票请求；其他节点B在更高任期4发起选举，A收到B的请求后更新自己的`currentTerm`为4，并降级为跟随者；此时A仍然可能收到任期3的投票回复（例如网络延迟导致回复晚到），如果无任期检查，A会错误地认为自己当选为领导者，出现多个leader错误。
2. 快速恢复时leader根据follower返回的信息（冲突任期的最早索引`ConflictIndex`和`ConflictIndex`）决定如何选择`newNextIndex`
   - Follower“4 5 5”和Leader“4 6 6 6”：Leader 的日志中没有 Follower 报告的冲突任期，`newNextIndex`直接从`ConflictIndex`开始覆盖
   - Follower"4 4 4"和Leader"4 6 6 6"：Leader 的日志中有 Follower 报告的冲突任期，`newNextIndex`直接从`ConflictIndex + 1`开始覆盖
   - Follower"4"和Leader"4 6 6 6"：Follower 的日志太短，`newNextIndex`应当从Follower日志的末尾开始尝试

### 容错的键值存储服务 (kvraft)

**难点与易错点：**

- 保证线性一致性：
  - **难点**：即使底层有Raft保证了操作的全局有序，上层KV服务也需要正确地与Raft交互，确保客户端观察到的行为符合线性一致性。例如，一个Get操作必须能读到在它开始之前所有已完成的Put/Append操作的结果。
  - **易错点**：Get操作直接读取本地状态而未通过Raft日志，可能导致读到旧数据（stale read），尤其是在发生Leader切换或网络分区后。
- 客户端请求的重复处理 (At-Most-Once Semantics)：
  - **难点**：客户端RPC可能会因为网络问题或Leader切换而超时重试。KV服务必须确保同一请求（即使由客户端多次发送）只被执行一次，否则可能导致数据错误（例如，对一个计数器的Append操作执行多次）。
  - **易错点**：重复请求检测机制设计不当，例如，仅根据请求内容判断是否重复，而没有唯一的请求标识符；或者，用于存储已处理请求信息的状态没有被正确持久化或在快照中包含，导致节点重启后重复执行。
- Leader 切换时的处理：
  - **难点**：当Raft集群发生Leader切换时，KV服务的RPC Handler可能已经将一个操作提交给了旧Leader的Raft实例，但该操作可能未被提交或客户端未收到响应。新Leader上任后，客户端会重试。KV服务需要优雅地处理这种情况，避免操作丢失或重复执行。
  - **易错点**：服务器在失去Leader身份后仍然处理请求；或者，服务器未能检测到自己已不是Leader，导致客户端长时间等待。
- 快照与状态机状态的一致性：
  - **难点**：KV服务在创建快照时，不仅需要包含键值数据，还需要包含用于实现at-most-once语义的客户端状态（如每个客户端已处理的最大序列号）。确保这些状态与Raft日志的快照点保持一致是关键。
  - **易错点**：快照中遗漏了客户端的 `lastSeqNum` 信息，导致重启后可能重复执行快照点之前的某些重试请求。

**代码中的解决方案分析 (`src/kvraft/server.go`)：**

- 保证线性一致性：
  - 所有的操作，包括 `Get`, `Put`, `Append`，都被封装成 `Op` 结构体（包含操作类型、Key、Value、ClientId、SeqNum） 。
  - 这些 `Op` 对象都通过调用底层Raft实例的 `rf.Start(*op)` 方法提交到Raft日志中 。这意味着即使是读操作 (Get) 也会走一遍Raft共识流程，确保了读取的是经过共识的最新状态。
  - 服务器通过一个 `applier` goroutine  持续从 `kv.applyCh` 读取Raft提交的 `ApplyMsg`。只有当操作通过Raft提交并在 `applyCh` 上出现时，服务器才真正执行该操作（如修改内存中的 `kv.store` ），并更新相关状态。
- 客户端请求的重复处理：
  - `KVServer` 结构体中有一个 `clientsStatus map[int64]*ClientStatus` 字段 。`ClientStatus` 结构体内部维护了 `lastSeqNum` ，记录了该客户端已成功执行的最新请求序列号。
  - 在 `operate` 函数中，处理一个新到来的操作前，会先检查 `clientStatus.done(op.SeqNum)` 。如果该序列号的操作已经被执行过，则直接返回（对于写操作）或基于当前状态返回（对于读操作），而不再通过Raft提交或执行。
  - 只有当操作是新的（`SeqNum` 更大）且通过Raft成功提交并应用后，`applyOp` 函数才会调用 `clientStatus.updateSeqNum(op.SeqNum)`  来更新该客户端的 `lastSeqNum`。
  - `ClientStatus` 内部还使用了 `sync.Cond` 。当一个操作通过 `rf.Start()` 提交后，RPC Handler会调用 `clientStatus.cond.Wait()` 等待该操作被应用。当 `applier` goroutine应用了该操作并更新了 `lastSeqNum` 后，会调用 `clientStatus.cond.Broadcast()` 来唤醒等待的RPC Handler。
- Leader 切换时的处理：
  - `kv.rf.Start()` 方法会返回当前Raft节点是否是Leader。如果不是Leader，`operate` 函数会直接返回错误 (ErrWrongLeader) ，客户端的 `Clerk` (在 `client.go` 中) 会捕获此错误并尝试联系其他服务器。
  - 即使一个操作被提交给了当时的Leader，但如果该Leader在操作被Raft commit之前崩溃或失去领导权，那么该 `Start()` 调用可能返回false，或者即使返回true，等待 `applyCh` 的过程也可能因为Leader变更而无法成功。客户端的重试机制和服务器端的重复请求检测机制共同确保操作的最终正确执行。
- 快照与状态机状态的一致性：
  - 当Raft日志大小超过 `maxraftstate` 时，`applier` goroutine在应用完一个命令后会调用 `kv.snapshot(m.CommandIndex)` 。
  - `snapshot` 方法会创建一个包含当前KV存储 (`kv.store.data`) 和所有客户端的 `lastSeqNum` 信息 (`kv.clientsSeqNum()`) 的快照字节流，然后调用 `kv.rf.Snapshot()` 。
  - `applySnap` 方法负责在Raft通知应用快照时，从快照数据中恢复 `kv.store` 和 `kv.clientsStatus` 。这样保证了即使发生日志截断和节点重启，KV服务的状态（包括重复请求检测所需的状态）也能被正确恢复。

### 分片的键值存储服务 (shardkv)

**难点与易错点：**

- 配置管理与同步：
  - **难点**：所有ShardKV副本组都需要及时获取并就最新的分片配置 (Configuration) 达成一致。配置的变更必须是有序的，并且所有副本组内的节点都必须在相同的逻辑时间点（相对于其他客户端请求）应用新的配置。
  - **易错点**：不同副本组或同一组内的不同节点使用了不同版本的配置，导致请求被路由到错误的组，或对同一分片产生不一致的操作。
- 分片迁移 (Shard Migration)：
  - **难点**：当配置发生变化，分片需要从一个副本组迁移到另一个副本组时，整个过程必须是原子和一致的。源组必须确保在迁移期间不再接受对该分片的新写操作（或将它们导向新组），目标组必须完整接收分片的所有数据以及相关的客户端状态（如 `lastSeqNum` 以维护at-most-once语义）。迁移过程中如果发生节点故障或网络分区，情况会更复杂。
  - **易错点**：分片数据在迁移过程中丢失或损坏；迁移完成后，源组和目标组对分片归属权认知不一致；迁移过程中客户端请求处理不当，导致数据不一致或操作丢失。
- 保证线性一致性跨分片和配置变更：
  - **难点**：在存在数据分片和动态配置变更的系统中维护线性一致性比单Raft组KV服务更难。客户端的一个操作可能因为配置变更而被重定向，系统必须保证该操作最终的效果与所有其他操作之间存在一个全局一致的顺序。
  - **易错点**：在配置变更的临界期，操作可能被错误地应用在旧的负责组或新的负责组，或者同时应用，破坏线性一致性。
- 处理孤儿请求 (Orphan Requests) 和重复请求：
  - **难点**：与kvraft类似，需要处理客户端重试。在分片系统中，一个请求可能先发送给一个组，由于配置变更，又被重试到另一个组。需要确保请求最终只被正确执行一次，即使它跨越了多个组和配置版本。
  - **易错点**：仅在单个副本组内实现重复请求检测，而没有考虑请求在组间迁移和配置变更过程中的全局唯一性。
- 组间通信的可靠性与协调：
  - **难点**：分片迁移需要不同副本组之间进行RPC通信来拉取/推送分片数据。这些RPC本身也可能失败或超时。
  - **易错点**：简单地认为组间RPC总能成功，没有处理RPC失败或超时的重试逻辑，导致迁移卡住。

**代码中的解决方案分析 (`src/shardkv/server.go`, `src/shardctrler/server.go`)：**

- 配置管理与同步：
  - `ShardCtrler` 服务 (`shardctrler/server.go`) 本身是一个基于Raft的容错服务，它负责管理和分发配置信息。配置以版本号 (`Num`) 递增 。
  - `ShardKV` 服务器 (`shardkv/server.go`) 内部有一个 `ctrlClerk *shardctrler.Clerk` ，用于定期 (`pollConfig` goroutine) 向 `ShardCtrler` 查询最新的配置 (`kv.ctrlClerk.Query(oldConfig.Num + 1)`) 。
  - 当 `ShardKV` 服务器检测到新的配置后，不会立即使用。而是将新的配置提案（例如 `SetConfigArgs`）通过其自己组内的Raft进行共识 。这意味着组内所有成员都会在Raft日志中的同一点看到并同意切换到新配置，从而保证了组内配置同步。这体现在 `applyOp` 中对 `SET_CONFIG` 类型的 `Op` 的处理，它会调用 `kv.setConfig(args.Config.Clone())` 。
- 分片迁移：
  - 当 `ShardKV` 服务器通过其Raft日志提交并应用了一个新的配置后，它会比较新旧配置，确定哪些分片需要迁入，哪些需要迁出。这在 `changeConfig` 函数和被其调用的 `migration` 函数中处理 。
  - **拉取数据 (Pull-based)**：对于需要迁入的分片，当前组（作为目标组）会向旧的负责组（源组）发送 `GetShardsArgs` RPC请求，以获取分片数据和相关的客户端 `lastSeqNum` 状态 (`callGetShardsFromGroup` 方法) 。
  - **应用数据**：收到数据后，目标组会将这些分片数据和客户端状态封装成一个 `PutShardsArgs` 操作，并通过自己组内的Raft提交这个操作 (`callPutShardsToGroup` 方法后，最终通过 `applyOp` 应用 `PUT_SHARDS`) 。这确保了数据原子地应用到组内所有副本。
  - **清理旧数据 (可选，但Lab有相关测试)**：迁移完成后，源组可能会被告知删除已迁出的分片数据（通过 `DeleteShardsArgs` RPC）。
  - 整个迁移过程（发现新配置、拉取分片、应用分片、更新配置状态）都作为Raft日志条目进行处理，确保了原子性和一致性。
- 保证线性一致性跨分片和配置变更：
  - 客户端请求首先根据 `key2shard()`  和当前 `ShardKV` 服务器持有的（已通过Raft共识的）配置，判断请求是否属于本组负责的分片。
  - 如果请求的key所属分片不归当前组管理（`kv.gid != kv.config.Shards[shard]` ），服务器会返回 `ErrWrongGroup` 。客户端 `Clerk` (在 `shardkv/client.go`) 收到此错误后，会向 `ShardCtrler` 查询最新配置，然后重试。
  - 在配置变更期间，一个组在尚未完成对某个分片的迁入（即未通过Raft日志应用包含该分片数据和状态的 `PUT_SHARDS` 操作）之前，不会接受对该分片的请求。一旦完成迁入并通过Raft确认新配置生效，它才会开始处理这些请求。
  - 所有操作（包括配置变更本身和数据迁移操作）都通过Raft日志保证了全局有序，这是实现线性一致性的基础。
- 处理孤儿请求和重复请求：
  - 与kvraft类似，`ShardKV` 也使用了 `ClientId` 和 `SeqNum` 来检测和过滤重复请求 (`clientStatusMap ClientStatusMap`  和 `ClientStatus` 结构体 )。
  - 关键在于，当分片数据从一个组迁移到另一个组时，与这些分片相关的客户端 `lastSeqNum` 状态也必须一同迁移 (`PutShardsArgs` 包含 `ClientSeqNumMap` ，并在 `applyOp` 中处理 `PUT_SHARDS` 时应用 `kv.clientStatusMap.putClientSeqNumMap(args.ClientSeqNumMap)` )。这确保了即使一个请求因为配置变更被重定向到新的负责组，新组也能根据迁移过来的 `lastSeqNum` 状态识别出这是否是一个重复请求。
- 组间通信的可靠性与协调：
  - `callOtherGroup` 方法  封装了向其他组发送RPC（如 `GetShards`, `PutShards`, `DeleteShards`）的逻辑。
  - 它会尝试联系目标组的所有服务器，直到有一个成功响应或者超时。它还维护了一个 `groupLeaderMap`  来缓存已知其他组的Leader，以优化RPC的发送。
  - 分片迁移的发起和数据应用都是通过本组的Raft日志进行的，这使得即使组间RPC暂时失败，本组的Raft状态也能保证操作最终会被重试或以一致的方式处理。

## 项目所用知识点总结

根据您的项目描述和代码实现，主要涉及以下技术知识：

- 编程语言与工具：

  - **Go语言**：深入运用了其并发原语（goroutines, channels, `sync`包中的Mutex, RWMutex, Cond）、RPC库 (`net/rpc`, `labrpc`)、序列化 (`encoding/json`, `labgob`)、文件操作等。

- 分布式系统核心概念：

  - **一致性协议 (Raft)**：领导者选举、日志复制、安全性（例如，选举限制、日志匹配特性）、持久化、日志压缩（快照）、集群成员变更（本项目中未要求，但Raft本身支持）。
  - **状态机复制 (Replicated State Machines)**：使用Raft来同步操作序列，使得所有副本以相同顺序执行相同操作，从而达到一致的状态。
  - **故障容错**：处理节点崩溃、网络分区、消息丢失/延迟/重复/乱序等。
  - **线性一致性 (Linearizability)**：为客户端提供“像单机一样”的强一致性保证。
  - **数据分片 (Sharding)**：将数据分散到多个独立的Raft组，以提高可伸缩性和性能。
  - **配置管理**：使用中心化的服务（Shard Controller）来管理和分发数据分片与副本组的映射关系。
  - **分布式事务/操作**：在分片迁移等场景下，确保多个步骤原子性或一致性地完成。
  
- 并行计算模型：

  - **MapReduce**：理解其基本工作流程、任务划分、中间数据处理、故障恢复。

- 软件工程实践：

  - **模块化设计**：将系统分解为MapReduce、Raft、KV服务、Sharding服务等多个可独立测试和理解的模块。
  - **测试驱动开发**：项目提供了大量测试用例，您需要通过这些测试来验证实现的正确性。
  - **并发控制**：正确使用锁等机制来避免竞态条件。
  - **RPC设计**：定义清晰的RPC接口和数据结构。
  - **持久化与序列化**：选择合适的持久化方案和序列化格式。