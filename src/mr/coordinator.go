package mr

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	UNASSIGNED int = iota
	ASSIGNED
	DONE
	TIMEDOUT
)

// 10 sec timeout for a task
const TIMEOUTSEC = 10

type Coordinator struct {
	// Your definitions here.
	cond      *sync.Cond
	available bool // whether any unassigned tasks

	nReduce int
	taskNum int

	mtask MapTask
	rtask ReduceTask
}

type MapTask struct {
	lock      sync.Mutex
	fileNum   int
	filenames []string
	mstate    []int // status of M map tasks
	done      bool
}

type ReduceTask struct {
	lock      sync.Mutex
	reduceNum int
	filenames []string
	rstate    []int // status of R reduce tasks
	done      bool
}

type CoordError struct {
	Err string
}

// Your code here -- RPC handlers for the worker to call.

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	fmt.Println("RPC server: received package from worker, req=", args.X)
	return nil
}

func (c *Coordinator) AssignTask(reqst *TaskRequest, reply *TaskReply) error {

	// Suspend the worker when no free tasks
	if !c.valid() {
		c.gotoSleep(reqst.Pid)
	}

	reply.NReduce = c.nReduce

	mdone := c.mapDone()
	rdone := c.reduceDone()

	// Check if all map tasks done
	if !mdone {

		c.mtask.lock.Lock()
		mapid := c.assignMap()

		// All map tasks assigned or done, please wait
		if mapid == -1 {
			// TODO: go to sleep
			// reply.TaskType = "wait"
			c.mtask.lock.Unlock()

			c.gotoSleep(reqst.Pid)
			reply.TaskType = "retry"
			return nil
		}

		// Assign a map task
		reply.TaskType = "map"
		reply.TaskId = mapid
		reply.Filepath = append(reply.Filepath, c.mtask.filenames[mapid])
		c.mtask.mstate[mapid] = ASSIGNED

		c.mtask.lock.Unlock()

		// Check timeout
		go func() {
			time.Sleep(TIMEOUTSEC * time.Second)
			c.mtask.lock.Lock()
			defer c.mtask.lock.Unlock()

			if c.mtask.mstate[mapid] != DONE {
				c.mtask.mstate[mapid] = TIMEDOUT
				fmt.Printf("Map task %v timed out\n", mapid)
				// wake up all workers
				c.enable()
				fmt.Printf("Waking up all workers...\n")
				c.cond.Broadcast()
			}

		}()

	} else if !rdone {
		// Assign a reduce task
		c.rtask.lock.Lock()

		reduceid := c.assignReduce()
		if reduceid == -1 {
			// reply.TaskType = "wait"
			c.rtask.lock.Unlock()

			c.gotoSleep(reqst.Pid)
			reply.TaskType = "retry"
			return nil
		}
		reducefiles := c.rtask.matchFiles(reduceid)
		if len(reducefiles) == 0 {
			log.Fatalf("Coordinator: cannot match reduce file of task %v\n", reduceid)
		}

		// Assign a reduce task
		reply.TaskType = "reduce"
		reply.TaskId = reduceid
		reply.Filepath = reducefiles
		c.rtask.rstate[reduceid] = ASSIGNED

		c.rtask.lock.Unlock()

		// Check timeout
		go func() {
			time.Sleep(TIMEOUTSEC * time.Second)
			c.rtask.lock.Lock()
			defer c.rtask.lock.Unlock()
			if c.rtask.rstate[reduceid] != DONE {
				c.rtask.rstate[reduceid] = TIMEDOUT
				fmt.Printf("Reduce task %v timed out\n", reduceid)
				// TODO: wake up all workers
				c.enable()
				fmt.Printf("Waking up all workers...\n")
				c.cond.Broadcast()
			}
		}()

	} else {
		reply.TaskType = "exit"
	}
	return nil
}

//
//	Notice coordinator that the task assigned was done by worker
//
func (c *Coordinator) NoticeTaskDone(notice *TaskNotice, reply *TaskReply) error {
	switch notice.TaskType {
	case "map":
		fmt.Printf("> Map task %v done from worker\n", notice.TaskId)

		c.mtask.lock.Lock()

		c.mtask.mstate[notice.TaskId] = DONE
		c.mtask.fileNum--

		// Add to reduce tasks
		c.rtask.lock.Lock()
		c.rtask.filenames = append(c.rtask.filenames, notice.OFilepath...)
		c.rtask.lock.Unlock()

		if c.mtask.fileNum == 0 {
			c.mtask.done = true
			fmt.Printf(">> Coordinator: All map tasks done.\n")
			fmt.Printf("Waking up all workers...\n")
			c.mtask.lock.Unlock()

			// wake up all sleeping workers
			c.enable()
			c.cond.Broadcast()
		} else {
			c.mtask.lock.Unlock()
		}

	case "reduce":
		fmt.Printf("> Reduce task %v done from worker\n", notice.TaskId)

		c.rtask.lock.Lock()

		c.rtask.rstate[notice.TaskId] = DONE
		c.rtask.reduceNum--

		if c.rtask.reduceNum == 0 {
			c.rtask.done = true
			fmt.Printf(">> Coordinator: All reduce tasks done\n")
			fmt.Printf("Waking up all workers...\n")
			c.rtask.lock.Unlock()

			c.enable()
			c.cond.Broadcast()
		} else {
			c.rtask.lock.Unlock()
		}
	}
	return nil
}

//
//	Not re-entrant, must hold c.mtask.lock when access
//	return -1 when all map tasks done or assigned
//
func (c *Coordinator) assignMap() int {
	flag := true
	for i, status := range c.mtask.mstate {
		if status == UNASSIGNED || status == TIMEDOUT {
			return i
		}
		flag = flag && (status == DONE)
	}

	if flag {
		// Map task done
		c.mtask.done = flag
	} else {
		// All assigned
		c.disable()
	}
	return -1
}

//
//	Not re-entrant, must hold c.rtask.lock when access
// 	return -1 when all reduce tasks done or assigned
//
func (c *Coordinator) assignReduce() int {
	flag := true
	for i, status := range c.rtask.rstate {
		if status == UNASSIGNED || status == TIMEDOUT {
			return i
		}
		flag = flag && (status == DONE)
	}

	if flag {
		c.rtask.done = flag
	} else {
		c.disable()
	}
	return -1
}

//
//	Put the worker to sleep when no available tasks
//
func (c *Coordinator) gotoSleep(wid int) {
	c.cond.L.Lock()
	if !c.available {
		fmt.Printf("Put worker %v to sleep\n", wid)
		c.cond.Wait()
	}
	c.cond.L.Unlock()
	fmt.Printf("Worker %v awaked\n", wid)
}

//
//	Match filenames with reduceId
//
func (r *ReduceTask) matchFiles(reduceid int) []string {
	pattern := fmt.Sprintf("mr-[0-9]+-%d", reduceid)
	regex := regexp.MustCompile(pattern)

	match := []string{}

	for _, filename := range r.filenames {
		found := regex.MatchString(filename)
		if found {
			match = append(match, filename)
		}
	}
	return match
}

func (c *Coordinator) mapDone() bool {
	c.mtask.lock.Lock()
	defer c.mtask.lock.Unlock()
	return c.mtask.done
}

func (c *Coordinator) reduceDone() bool {
	c.rtask.lock.Lock()
	defer c.rtask.lock.Unlock()
	return c.rtask.done
}

func (c *Coordinator) valid() bool {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	return c.available
}

func (c *Coordinator) enable() {
	c.cond.L.Lock()
	c.available = true
	c.cond.L.Unlock()
}
func (c *Coordinator) disable() {
	c.cond.L.Lock()
	c.available = false
	c.cond.L.Unlock()
}

func (e *CoordError) Error() string {
	return fmt.Sprintf("%v\n", e.Err)
}

//
// start a thread that listens for RPCs from worker.go
//
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

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	ret := false

	// Your code here.
	ret = c.mapDone() && c.reduceDone()

	return ret
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{}

	// Your code here.
	c.cond = sync.NewCond(&sync.Mutex{})
	c.nReduce = nReduce
	c.taskNum = 0
	c.available = true

	// Read original files for map tasks
	inputfilePattern := "../pg*txt"
	filenames, err := filepath.Glob(inputfilePattern)
	if err != nil {
		log.Fatalf("cannot open %v", filenames)
	}
	c.mtask.filenames = filenames
	c.mtask.fileNum = len(filenames)
	c.mtask.mstate = make([]int, len(filenames))
	c.mtask.done = false
	// fmt.Println(filenames)
	// fmt.Println(c.mtask.mstatus)

	// Initialize reduce tasks
	c.rtask.reduceNum = nReduce
	c.rtask.rstate = make([]int, nReduce)
	c.rtask.done = false

	c.server()
	return &c
}
