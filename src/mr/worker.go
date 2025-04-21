package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
	"sort"
)

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// for sorting by key.
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	for {
		if reply, ok := callFetchTask(); ok {
			// fmt.Printf("reply %v\n", reply)
			if reply.Done {
				return
			}
			if reply.MapTask != nil {
				doMapTask(reply.MapTask, mapf)
			}
			if reply.ReduceTask != nil {
				doReduceTask(reply.ReduceTask, reducef)
			}
		}
	}
}

func doMapTask(task *MapTask, mapf func(string, string) []KeyValue) {
	// 1. 读取输入文件
	file, err := os.Open(task.FileName)
	if err != nil {
		log.Fatalf("cannot open %v", task.FileName)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", task.FileName)
	}
	file.Close()

	// 2. 调用用户定义的 Map 函数
	kva := mapf(task.FileName, string(content))

	// 3. 按 Reduce 任务数分片
	omap := make([][]KeyValue, task.NReduce)
	for _, kv := range kva {
		i := ihash(kv.Key) % task.NReduce
		omap[i] = append(omap[i], kv)
	}

	// 4. 将分片写入中间文件（原子操作）
	for i, ikva := range omap {
		intermediateFileName := fmt.Sprintf("mr-%d-%d.m", task.Id, i)
		tmpfile, err := os.CreateTemp("./", "mr-*.tmp") // 创建临时文件
		if err != nil {
			log.Fatal(err)
		}
		encoder := json.NewEncoder(tmpfile)
		for _, kv := range ikva {
			err := encoder.Encode(&kv) // JSON 编码写入
			if err != nil {
				log.Fatalf("cannot jsonencode %v", kv)
			}
		}
		tmpfile.Close()
		if err := os.Rename(tmpfile.Name(), intermediateFileName); err != nil {
			log.Fatal(err)
		}
	}

	callMapTaskFinished(task.Id) // 通知协调者 Map 任务完成
}

func doReduceTask(task *ReduceTask, reducef func(string, []string) string) {
	intermediate := []KeyValue{}
	// 1. 收集所有 Map 任务生成的对应分片文件
	for i := 0; i < task.NMap; i++ {
		intermediateFileName := fmt.Sprintf("mr-%d-%d.m", i, task.Id)
		file, err := os.Open(intermediateFileName)
		if err != nil {
			log.Fatalf("cannot open %v", intermediateFileName)
		}
		decoder := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := decoder.Decode(&kv); err != nil {
				break
			}
			intermediate = append(intermediate, kv)
		}
	}

	// 2. 按 Key 排序
	sort.Sort(ByKey(intermediate))

	// 3. 调用用户定义的 Reduce 函数并写入结果
	tmpfile, err := os.CreateTemp("./", "mr-*.tmp")
	if err != nil {
		log.Fatal(err)
	}
	//
	// call Reduce on each distinct key in intermediate[],
	// and print the result to mr-out-id.
	//
	i := 0
	for i < len(intermediate) {
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		output := reducef(intermediate[i].Key, values)

		// this is the correct format for each line of Reduce output.
		fmt.Fprintf(tmpfile, "%v %v\n", intermediate[i].Key, output)

		i = j
	}

	tmpfile.Close()
	if err := os.Rename(tmpfile.Name(), fmt.Sprintf("mr-out-%d.r", task.Id)); err != nil {
		log.Fatal(err)
	}
	callReduceTaskFinished(task.Id)
}

func callFetchTask() (*FetchTaskReply, bool) {
	args := FetchTaskArgs{}
	reply := FetchTaskReply{}
	ok := call("Coordinator.FetchTask", &args, &reply)
	return &reply, ok

}

func callMapTaskFinished(taskId int) (*TaskFinishedReply, bool) {
	args := TaskFinishedArgs{taskId}
	reply := TaskFinishedReply{}
	ok := call("Coordinator.MapTaskFinished", &args, &reply)
	return &reply, ok
}

func callReduceTaskFinished(taskId int) (*TaskFinishedReply, bool) {
	args := TaskFinishedArgs{taskId}
	reply := TaskFinishedReply{}
	ok := call("Coordinator.ReduceTaskFinished", &args, &reply)
	return &reply, ok
}

// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
func callExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
