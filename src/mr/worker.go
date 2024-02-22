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
	"time"
)

//
// Map functions return a slice of KeyValue.
//
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

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.

	fmt.Printf("Worker %v available\n", os.Getpid())
	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

	// Loop to require tasks from coordinator

	for {
		task := RequireTask()
		if task == nil {
			return
		}
		fmt.Printf("task: %v\n", task)
		switch task.TaskType {
		case "map":
			doMap(task, mapf)

		case "reduce":
			doReduce(task, reducef)

		case "retry":
			continue

		case "wait":
			time.Sleep(3 * time.Second)

		case "exit":
			return

		default:
		}
	}

}

func RequireTask() *TaskReply {
	reqst := new(TaskRequest)
	reply := new(TaskReply)

	ok := call("Coordinator.AssignTask", reqst, reply)
	if ok {
		fmt.Printf("get %v task %v\n", reply.TaskType, reply.TaskId)
	} else {
		fmt.Printf("get task failed\n")
		reply = nil
	}
	return reply
}

func doMap(task *TaskReply, mapf func(string, string) []KeyValue) {

	file, err := os.Open(task.Filepath[0])
	if err != nil {
		log.Fatalf("worker: cannot open %v\n", task.Filepath)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("worker: cannot read %v\n", task.Filepath)
	}

	// Divide into nReduce intermediate files
	kva := mapf(task.Filepath[0], string(content))

	sort.Sort(ByKey(kva))

	oname := make([]string, task.NReduce)
	ofile := make([]*os.File, task.NReduce)
	enc := make([]*json.Encoder, task.NReduce)
	prefix := fmt.Sprintf("mr-%d-", task.TaskId)

	for y := 0; y < task.NReduce; y++ {
		// TODO: Replace them with temp files first
		oname[y] = fmt.Sprint(prefix, y)
		ofile[y], _ = os.Create(oname[y])
		defer ofile[y].Close()
		// fmt.Printf("worker: writing %v...\n", oname)
		enc[y] = json.NewEncoder(ofile[y])

	}
	for _, kv := range kva {
		reduceId := ihash(kv.Key) % task.NReduce
		err := enc[reduceId].Encode(&kv)
		if err != nil {
			log.Fatalf("worker: writing intermediate file failed\n")
		}
	}

	// Send notice to coordinator
	notice := new(TaskNotice)
	notice.TaskId = task.TaskId
	notice.TaskType = "map"
	notice.OFilepath = append(notice.OFilepath, oname...)
	call("Coordinator.NoticeTaskDone", notice, nil)
}

func doReduce(task *TaskReply, reducef func(string, []string) string) {

}

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

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

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
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
