package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

type TaskState int

const (
	WAITTING TaskState = 0 // 等待分配
	STARTED  TaskState = 1 // 已分配但未完成
	FINISHED TaskState = 2 // 已完成
)

type Coordinator struct {
	// Your definitions here.
	Mutex        sync.Mutex    // 全局互斥锁
	Cond         *sync.Cond    // 条件变量（用于 Worker 等待）
	mapTasksDone bool          // Map 阶段是否完成
	done         bool          // 全部任务是否完成
	MapTasks     []*MapTask    // Map 任务列表
	ReduceTasks  []*ReduceTask // Reduce 任务列表
}

type MapTask struct {
	Id       int       // 任务 ID
	FileName string    // 输入文件名
	NReduce  int       // Reduce 任务数（用于分片）
	State    TaskState // 当前状态
}

type ReduceTask struct {
	Id    int       // 任务 ID
	NMap  int       // Map 任务数（用于收集分片）
	State TaskState // 当前状态
}

// Your code here -- RPC handlers for the worker to call.
func (c *Coordinator) FetchTask(args *FetchTaskArgs, reply *FetchTaskReply) error {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()

	for {
		if c.MapTasksDone() { // 优先分配 Map 任务
			break
		} else if task := c.fetchMapTask(); task != nil {
			reply.MapTask = task
			c.mapTaskStarted(task) // 标记为 STARTED 并启动超时恢复
			// log.Printf("fetch map task %d\n", task.Id)
			return nil
		} else {
			c.Cond.Wait() // 无任务可用， Worker 等待
		}
	}

	// Map 阶段完成，开始分配 Reduce 任务
	for {
		if c.Done() {
			reply.Done = true
			break
		} else if task := c.fetchReduceTask(); task != nil {
			reply.ReduceTask = task
			c.reduceTaskStarted(task) // 标记为 STARTED 并启动超时恢复
			// log.Printf("fetch reduce task %d\n", task.Id)
			return nil
		} else {
			c.Cond.Wait() // 无任务可用， Worker 等待
		}
	}

	return nil
}

func (c *Coordinator) fetchMapTask() *MapTask {
	for _, task := range c.MapTasks {
		if task.State == WAITTING {
			return task
		}
	}
	return nil
}

func (c *Coordinator) fetchReduceTask() *ReduceTask {
	for _, task := range c.ReduceTasks {
		if task.State == WAITTING {
			return task
		}
	}
	return nil
}

// 标记 Map 任务为 STARTED，并启动超时恢复
func (c *Coordinator) mapTaskStarted(task *MapTask) {
	task.State = STARTED
	// recovery function
	go func(task *MapTask) {
		timedue := time.After(10 * time.Second) // 创建一个会在10s后接收到时间信号的通道
		<-timedue                               // 阻塞当前 goroutine，直到接收到时间信号
		c.Mutex.Lock()                          // 获取全局锁，保证修改任务状态时不会冲突
		defer c.Mutex.Unlock()
		if task.State != FINISHED {
			log.Printf("recover map task %d \n", task.Id)
			task.State = WAITTING // 超时未完成，重置状态
			c.Cond.Broadcast()    // 唤醒等待的 Worker
		}
	}(task)
}

// 标记 Reduce 任务为 STARTED，并启动超时恢复
func (c *Coordinator) reduceTaskStarted(task *ReduceTask) {
	task.State = STARTED
	// recovery function
	go func(task *ReduceTask) {
		timedue := time.After(10 * time.Second)
		<-timedue
		c.Mutex.Lock()
		defer c.Mutex.Unlock()
		if task.State != FINISHED {
			log.Printf("recover reduce task %d \n", task.Id)
			task.State = WAITTING
			c.Cond.Broadcast()
		}
	}(task)

}

// 标记 Map 任务完成
func (c *Coordinator) MapTaskFinished(args *TaskFinishedArgs, reply *TaskFinishedReply) error {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.MapTasks[args.TaskId].State = FINISHED
	if c.MapTasksDone() {
		c.Cond.Broadcast()
	}
	return nil
}

// 标记 Reduce 任务完成
func (c *Coordinator) ReduceTaskFinished(args *TaskFinishedArgs, reply *TaskFinishedReply) error {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.ReduceTasks[args.TaskId].State = FINISHED
	if c.Done() {
		c.Cond.Broadcast()
	}
	return nil
}

// start a thread that listens for RPCs from worker.go
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

func (c *Coordinator) MapTasksDone() bool {
	if c.mapTasksDone {
		return true
	}
	for _, task := range c.MapTasks {
		if task.State != FINISHED {
			return false
		}
	}
	c.mapTasksDone = true
	return true
}

// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
func (c *Coordinator) Done() bool {
	// Your code here.
	if c.done {
		return true
	}
	for _, task := range c.ReduceTasks {
		if task.State != FINISHED {
			return false
		}
	}
	c.done = true
	return true
}

// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}
	c.Cond = sync.NewCond(&c.Mutex)
	// c.reduceTasksCond = sync.NewCond(&c.reduceTasksMutex)
	// Your code here.
	c.MapTasks = make([]*MapTask, 0)
	for i, filename := range files {
		task := &MapTask{
			Id:       i,
			FileName: filename,
			NReduce:  nReduce,
			State:    WAITTING,
		}
		c.MapTasks = append(c.MapTasks, task)
	}

	c.ReduceTasks = make([]*ReduceTask, 0)
	for i := 0; i < nReduce; i++ {
		task := &ReduceTask{
			Id:    i,
			NMap:  len(files),
			State: WAITTING,
		}
		c.ReduceTasks = append(c.ReduceTasks, task)
	}

	c.server()

	return &c
}
