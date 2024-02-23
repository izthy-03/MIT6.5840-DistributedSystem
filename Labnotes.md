# MIT 6.824 Lab notes
<hr/>

## <center> Lab1. MapReduce <center/>
<p align="right">2024.2.23<p>

刚拿到任务描述和项目代码，各种Rules, Hints满天飞，一时有点无从下手

看来看去，我决定从第一条hint开始：让worker通过RPC向Coordinator发请求

注意到源码提供了`CallExample()`，简单学习了一下GO的RPC机制，再结合MapReduce要做的事，大体框架就逐渐清晰了：`mrworker`创建Worker不断向`mrcoordinator`创建的Coordinator发送任务请求，Coordinator将任务要求和任务文件返回，所有任务完成后，`Coordinator.Done()`将返回`true`给`mrcoordinator`

首先从RPC入手

定义RPC任务请求结构`TaskRequest`和任务回应结构`TaskReply`。注意RPC通信时也只会发送首字母大写的字段

```go
// Add your RPC definitions here.

type TaskRequest struct {
	Pid int
}

type TaskReply struct {
	TaskId   int
	TaskType string // map, reduce, exit, retry
	Filepath []string
	NReduce  int
}
```

受hint 
> Depending on your design, you might also find it helpful to have a "please exit" pseudo-task that the coordinator can give to workers.

启发，我定义了`exit`和`retry`两个伪任务，便于worker退出

完成了简单的Worker hello和Coordinator hello后，便正式开始MapReduce。

### 阶段1 - Map

首先要定义Coordinator的各个字段，我针对map阶段创建了一个结构体`MapTask`，里面保存了map阶段的文件数、文件名、任务状态等字段以及一把锁，类似同样定义一个`ReduceTask`结构体（好像可以复用哦（逃）

```go
type Coordinator struct {
	// Your definitions here.
	nReduce int
	taskNum int

	mtask MapTask
	rtask ReduceTask
}

type MapTask struct {
	lock      sync.Mutex
	fileNum   int
	filenames []string
	mstate    []int // states of M map tasks
	done      bool
}

type ReduceTask struct {
	lock      sync.Mutex
	reduceNum int
	filenames []string
	rstate    []int // states of R reduce tasks
	done      bool
}
```

阅读框架代码发现，coordinator.go从`MakeCoordinator(files []string, nReduce int)`开始被调用，初始化读取任务文件等数据，注册启动RPC服务后，Coordinator就能开始响应Worker请求了

于是我注册了一个`Coordinator.AssignTask`方法，雏形如下

如果map阶段还未结束，那么就分配map任务，由`assignMap()`寻找空闲的map task并返回`mapid`，并将这个任务返回给worker，皆大欢喜


```go
// Prototype, uncompleted
func (c *Coordinator) AssignTask(reqst *TaskRequest, reply *TaskReply) error {
    ...
	mdone := c.mapDone()
	rdone := c.reduceDone()

	// Check if all map tasks done
	if !mdone {

		c.mtask.lock.Lock()
		mapid := c.assignMap()

		// All map tasks assigned or done, please wait
		if mapid == -1 {
			c.mtask.lock.Unlock()
            ...
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
            ...
        }

	} else if !rdone {
        // Assign reduce task
        ...
	} else {
        // Pseudo-task
		reply.TaskType = "exit"
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
            // 👇 Just assign this task 
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

```

如果`assignMap()`返回-1，要么就是map任务分配完了，要么就是map阶段结束了，不管怎样，这个worker都要回去等通知

简单的实现就是返回一个"wait"伪任务，让worker返回自己的线程睡三秒再来请求
```go
    // Coordinator:
        ...
		// All map tasks assigned or done, please wait
		if mapid == -1 {
			reply.TaskType = "wait"
			c.mtask.lock.Unlock()
			return nil
		}
        ...
    // Worker:
        ...
        case "wait":
        time.Sleep(3 * time.Second)
        continue
        ...
```
这种方法简单有效，不易出错，缺点是在平均任务时间花费很短时，会让worker睡眠过长，降低效率。在所有测试通过后，我将其优化为了条件变量实现

```go
        ...
		// All map tasks assigned or done, please wait
		if mapid == -1 {
			c.mtask.lock.Unlock()

			c.gotoSleep(reqst.Pid)
			reply.TaskType = "retry"
			return nil
		}
        ...
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
	fmt.Printf("Worker %v awakened\n", wid)
}
```

接下来考虑Worker完成任务时通知Coordinator

定义RPC通信结构体`TaskNotice`并注册服务`Coordinator.NoticeTaskDone`

当Worker完成任务后返回任务信息和输出文件路径（hint提到：本lab中因为master和worker在同一台机器上，所以可以直接读取文件，但是在分布式情况下，需要一个全局文件系统，例如GFS）

Coordinator收到通知后修改对应任务的状态，并保存中间文件到`ReduceTask`备用，顺便判断一下所有map任务是否都完成了，是的话就可以修改`c.mtask.done = true`，进入reduce阶段

```go
type TaskNotice struct {
	TaskId    int
	TaskType  string   // map, reduce
	OFilepath []string // output file
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
		...
	}
	return nil
}
```
`c.cond.Broadcast()`也是对应的条件变量优化，用来唤醒前面提到的入睡的worker。如果是简单的单线程等待三秒实现，就不需要唤醒

这样Coordinator这边就剩一个关键问题：worker超时。论文提到，如果出现slow worker或者worker宕机，分配给这个worker的任务就要全部交给其他worker重做。

题目给出的超时时间是10秒，在分配了一个任务后，利用`go`原语和匿名函数，可以很方便地创建一个检查线程，在10秒后检查该任务状态

```go
	if !mdone {	

		...

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
	}
```
这样Coordinator这边的任务管理基本就完成了，下面是Worker要干的任务落实

worker首先要分类拿到的任务类型，主框架如下

```go
//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	fmt.Printf("Worker %v available\n", os.Getpid())

	// Loop to require tasks from coordinator
	for {
		task := RequireTask()
		fmt.Printf("	task: %v\n", task)
		if task == nil {
			return
		}
		
		switch task.TaskType {
		case "map":
			doMap(task, mapf)

		case "reduce":
			doReduce(task, reducef)

		case "retry":
			continue

		case "wait":
			time.Sleep(3 * time.Second)
			continue

		case "exit":
			return

		default:
		}
	}

}
```

得到map类型任务，就分流去执行`doMap()`

阅读框架代码和`mrsequential.go`后发现，mrworker通过`loadPlugin()`来动态加载以插件形式编译的`wc.go`中的`Map()`和`Reduce()`函数，这两个函数就是用户层面定义的任务，并以函数变量`mapf`和`reducef`返回给worker，worker只要将任务文件喂给它然后保存到输出文件就行了

又因
> You can steal some code from mrsequential.go for reading Map input files, for sorting intermedate key/value pairs between the Map and Reduce, and for storing Reduce output in files.

开偷！

```go

func doMap(task *TaskReply, mapf func(string, string) []KeyValue) {

	file, err := os.Open(task.Filepath[0])
	if err != nil {
		log.Fatalf("Worker: cannot open %v\n", task.Filepath)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		log.Fatalf("Worker: cannot read %v\n", task.Filepath)
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
```
和mrsequential实现不同的是，worker需要把输出文件划分成`nReduce`个中间文件，而同一个字段需要在同一个Y编号的中间文件(mr-X-Y)中，否则会导致最终结果出现重复字段，因此需要取每个Key做哈希再模`nReduce`，就用框架提供的`ihash()`。

题目其实还要求在彻底完成map任务前将输出保存在临时文件中，完成后再重命名
> To ensure that nobody observes partially written files in the presence of crashes, the MapReduce paper mentions the trick of using a temporary file and atomically renaming it once it is completely written. You can use ioutil.TempFile (or os.CreateTemp if you are running Go 1.17 or later) to create a temporary file and os.Rename to atomically rename it.

但这里我偷懒了（跑），没有这么做，但是从正确性来讲大概没什么问题，因为我在全部文件写完毕后再发送的通知，如果Coordinator没收到，那么也不会去访问这些中间文件，从而确保崩溃一致性，可以认为是一种log write-ahead-rule吧（笑）。

（PS：不过如果coordinator也崩溃了估计就不能保证一致性了，但据论文，coordinator挂了基本就得让用户重新执行整个MapReduce）

### 阶段2 - Reduce

和map阶段代码基本一致，不同的是一个worker要同时接收一组reduce文件，我在这里加了一个文件名匹配

```go
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
```
Worker这边要注意，如果直接复制mrsequential.go的代码，则要在读取完一组reduce文件后要先排序一下键值对，否则输出文件中会有重复的Key


### 总结
第一个Lab思路上挺清晰明了，debug也没太大痛苦，基本功能实现了，共享数据保护好了，所有测试就能一遍过，就是刚接触Go，上手有点生涩，而且代码有一大半重复了，感觉可以压缩复用一下；同时在最后改条件变量优化的时候字段定义和代码逻辑和之前写的有点交叉，大抵是一开始没想好的原因