package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import (
	"os"
	"strconv"
)

//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

type FetchTaskArgs struct {
}

// 协调者返回的任务信息
type FetchTaskReply struct {
	Done       bool // 全部任务是否完成
	MapTask    *MapTask
	ReduceTask *ReduceTask
}

type TaskFinishedArgs struct {
	TaskId int
}
type TaskFinishedReply struct {
}

// Add your RPC definitions here.

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
// 生成协调者的 Unix 套接字路径
func coordinatorSock() string {
	s := "/var/tmp/5840-mr-"
	s += strconv.Itoa(os.Getuid()) // 根据用户ID生成唯一路径
	return s
}
