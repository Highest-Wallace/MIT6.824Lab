"在MIT 6.824课程的第一个实验中，我实现了一个简化版的MapReduce并行计算框架。这个项目的目标是理解大规模数据处理的基本原理，以及如何构建一个能够容忍工作节点（Worker）故障的分布式系统。

**我的实现主要包含两个核心组件：**

1. **Coordinator（协调者）：** 这是中央控制节点，负责：
   - **任务划分与分配：** 将输入文件集划分为多个独立的Map任务，并将中间结果的键空间划分为指定数量的Reduce任务。它会按顺序先分配所有Map任务，待Map阶段完成后，再分配Reduce任务。
   - **任务状态跟踪：** Coordinator会记录每个Map任务和Reduce任务的状态（例如，等待分配、正在执行、已完成）。这部分逻辑主要在`src/mr/coordinator.go`中的`Coordinator`结构体及其方法中实现。
   - **故障容错：** 这是MapReduce框架的一个关键特性。我的Coordinator能够检测到长时间未完成任务的Worker（可能是因为Worker崩溃或网络问题）。具体做法是，当一个任务被分配给Worker后，Coordinator会启动一个**定时器（例如10秒）**。如果超过预设时间任务仍未被标记为完成，Coordinator会**将该任务重新置为“等待分配”状态**，以便其他健康的Worker可以接手。这个超时恢复逻辑在`mapTaskStarted`和`reduceTaskStarted`函数内部的goroutine中实现。
   - **RPC服务：** Coordinator通过Go的RPC机制与Worker节点通信，提供诸如`FetchTask`（Worker获取任务）和`TaskFinished`（Worker报告任务完成）等接口。
2. **Worker（工作节点）：** Worker是实际执行计算任务的进程，它会：
   - **循环请求任务：** Worker启动后会通过RPC向Coordinator请求任务。
   - **执行Map任务：** 如果领到Map任务，Worker会读取指定的输入文件，调用用户提供的`mapf`函数处理数据，并将产生的中间键值对通过哈希函数（`ihash`）分配到不同的桶（对应Reduce任务的数量）。这些中间结果会**原子地写入本地文件系统**，文件名遵循特定格式（如 `mr-X-Y`，X为Map任务ID，Y为Reduce任务ID）。为了保证写入的原子性，我采用了先写入临时文件，完成后再重命名为正式文件名的方法。
   - **执行Reduce任务：** 如果领到Reduce任务，Worker会根据任务ID找到所有相关的Map任务产生的中间文件，读取并排序这些键值对。然后，对每个唯一的键，调用用户提供的`reducef`函数聚合其所有值，并将最终结果写入输出文件（如 `mr-out-X`）。
   - 这部分逻辑主要在`src/mr/worker.go`的`Worker`函数以及相关的`doMapTask`和`doReduceTask`函数中。

**主要挑战与学习：** 这个实验让我对分布式任务调度、状态管理、并发控制（Coordinator需要使用互斥锁`sync.Mutex`和条件变量`sync.Cond`来安全地管理任务状态和协调Worker）以及基本的故障处理机制有了深入的实践。特别是Worker故障的超时重试机制，以及如何确保中间文件和输出文件的正确生成，是实现过程中的关键点。通过这个实验，我熟悉了Go语言在构建分布式应用方面的基本工具和模式。"

### Lab 1 (MapReduce) - 面试官可能询问的知识点及扩展考察

**1. Coordinator 的设计与实现 (`src/mr/coordinator.go`)**

- **问题：** “Coordinator如何跟踪任务的状态？它是如何保证并发安全的？”
  - **回答思路：** 解释`Coordinator`结构体中的`MapTasks`、`ReduceTasks`切片以及每个任务对象内的`State`字段。强调所有对这些共享状态的访问都通过全局互斥锁`Mutex`保护。可以提及`sync.Cond`用于Worker在无任务时等待。
- **问题：** “Worker故障恢复机制具体是如何工作的？超时时间是如何设定的？如果超时时间设得太短或太长会有什么影响？”
  - **回答思路：** 详细描述`mapTaskStarted`/`reduceTaskStarted`中的超时goroutine。超时时间（10秒）是实验指导中建议的。
    - 太短：可能将正常执行但稍慢的任务误判为失败，导致不必要的重试和资源浪费。
    - 太长：导致真正的Worker故障被发现得较晚，整体作业完成时间变长。
- **问题：** “Coordinator本身是单点，如果Coordinator崩溃了怎么办？这个实验有处理吗？如果没有，你会如何设计使其高可用？”
  - **回答思路：** 承认实验中的Coordinator是单点，未处理其自身故障。
    - **扩展思考：** 可以引入备用Coordinator（standby master），或者使用像ZooKeeper这样的协调服务来选举新的Coordinator，甚至将Coordinator本身设计成一个基于Raft的小集群（这就引向了后续实验）。
- **问题：** “当所有Map任务完成后，Coordinator如何通知Worker开始Reduce任务？”
  - **回答思路：** Coordinator内部有状态标记（如`mapTasksDone`）。当`FetchTask`被调用时，Coordinator会检查此状态。如果Map任务已全部完成，它会开始分配Reduce任务。`sync.Cond`的`Broadcast`会在Map阶段完成时唤醒可能在等待的Worker。

**2. Worker 的设计与实现 (`src/mr/worker.go`)**

- **问题：** “Worker如何保证Map任务产生的中间文件对Reduce任务是可见且一致的？特别是原子性写入是如何实现的？”
  - **回答思路：** 解释中间文件的命名规范 (`mr-X-Y`)。强调先写入临时文件，然后通过`os.Rename`原子性地重命名为最终文件名，以避免Reduce Worker读到不完整的文件。
- **问题：** “Map的输出是如何分发给不同的Reduce任务的？”
  - **回答思路：** 解释`ihash(key) % NReduce`的哈希分区策略，确保相同的键会被同一个Reduce Worker处理。
- **问题：** “如果一个Worker在执行Reduce任务时失败了，会发生什么？”
  - **回答思路：** 与Map任务类似，Coordinator也会对Reduce任务设置超时。如果Reduce Worker失败，其任务会被重新分配。因为Reduce任务的输入（中间文件）是确定的，所以重试是安全的。
- **问题：** “Worker如何知道整个MapReduce作业已经完成了并可以退出了？”
  - **回答思路：** 当Coordinator的`Done()`方法返回`true`（即所有Reduce任务都已完成）时，它可以向请求任务的Worker返回一个特殊的信号（例如，在`FetchTaskReply`中设置一个`Done`标志位），Worker收到此信号后即可退出。

**3. MapReduce 整体概念与扩展**

- **问题：** “MapReduce模型的核心思想是什么？它适合解决什么类型的问题？”
  - **回答思路：** 核心思想是“分而治之”。将大规模数据处理任务分解为许多独立的Map阶段和Reduce阶段。适合数据密集型、可并行处理的任务，如词频统计、排序、索引构建等。
- **问题：** “除了Worker故障，MapReduce还可能遇到哪些其他类型的故障或性能瓶颈？（例如stragglers - 掉队者问题）”
  - **回答思路：**
    - **Stragglers（掉队者）：** 某些任务由于机器性能、负载、坏盘等原因执行得异常缓慢，拖慢整个作业。Google的MapReduce通过推测执行（speculative execution）来缓解这个问题，即为可能掉队的任务启动一个备份任务，谁先完成就用谁的结果。
    - **数据倾斜 (Data Skew)：** 某些键的中间数据量远大于其他键，导致少数Reduce任务负载过重。
    - **网络瓶颈：** 在shuffle阶段（Map输出传输给Reduce输入）可能会有大量网络传输。
- **问题：** “你认为这个实验实现的MapReduce与Google论文中描述的MapReduce有哪些主要简化或不同？”
  - **回答思路：**
    - **Coordinator容错：** 实验中Coordinator是单点。
    - **推测执行：** 实验中未实现。
    - **数据本地性优化：** 实验中可能没有充分考虑将计算任务调度到数据所在的节点。
    - **更复杂的资源管理和调度：** 实际系统通常与YARN或Borg等集群管理器集成。
    - **更丰富的API和数据格式支持。**

**4. Go语言相关**

- **问题：** “在实现Coordinator时，为什么选择使用`sync.Mutex`和`sync.Cond`？它们各自解决了什么问题？”
  - **回答思路：** `Mutex`用于保护对共享任务状态的互斥访问，防止竞态条件。`Cond`用于在特定条件不满足时（如无可用任务）让goroutine高效等待，并在条件满足时被唤醒，避免了忙等待。
- **问题：** “RPC是如何在Coordinator和Worker之间工作的？你定义了哪些RPC消息？”
  - **回答思路：** 解释Go的`net/rpc`包。提及你定义的`FetchTaskArgs`/`Reply`, `TaskFinishedArgs`/`Reply`等结构体。

通过准备这些问题，您可以更好地展示您对MapReduce原理、分布式系统设计以及Go语言并发编程的理解。