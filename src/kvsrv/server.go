package kvsrv

import (
	"log"
	"sync"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type KVServer struct {
	mu sync.Mutex

	// Your definitions here.
	store     map[string]string
	clientNum int

	duptable   map[int64]*dupEntry
	dupReaders int
	dupRLock   sync.Mutex
	dupWLock   sync.Mutex
}

type dupEntry struct {
	seqid int
	value string
}

func (kv *KVServer) dupCheck(clientid int64, seqid int) (string, bool) {
	// RW model
	kv.dupRLock.Lock()
	kv.dupReaders++
	if kv.dupReaders == 1 {
		kv.dupWLock.Lock()
	}
	kv.dupRLock.Unlock()

	readerLeave := func() {
		kv.dupRLock.Lock()
		kv.dupReaders--
		if kv.dupReaders == 0 {
			kv.dupWLock.Unlock()
		}
		kv.dupRLock.Unlock()
	}

	defer readerLeave()

	entry, ok := kv.duptable[clientid]
	if !ok {
		// New client connection
		DPrintf("New client %v connected\n", clientid)
		return "", false
	} else if entry.seqid < seqid {
		// New RPC seq id
		return "", false
	} else if entry.seqid == seqid {
		// Duplicated
		return entry.value, true
	}
	return "", false
}

//
//	Not re-entrant
//
func (kv *KVServer) dupUpdate(clientid int64, seqid int, value string) {
	kv.dupWLock.Lock()
	defer kv.dupWLock.Unlock()

	_, ok := kv.duptable[clientid]
	if !ok {
		kv.clientNum++
		kv.duptable[clientid] = new(dupEntry)
		DPrintf("%v clients connected\n", kv.clientNum)
	}
	kv.duptable[clientid].seqid = seqid
	kv.duptable[clientid].value = value
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()

	reply.Value = kv.store[args.Key]
}

func (kv *KVServer) Put(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	if val, dup := kv.dupCheck(args.Clientid, args.Seqid); dup {
		reply.Value = val
		return
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	kv.store[args.Key] = args.Value
	reply.Value = ""

	// DPrintf("client %v seqid %v putting (%v, %v)\n", args.Clientid, args.Seqid, args.Key, args.Value)

	kv.dupUpdate(args.Clientid, args.Seqid, reply.Value)
}

func (kv *KVServer) Append(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	if val, dup := kv.dupCheck(args.Clientid, args.Seqid); dup {
		reply.Value = val
		return
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()

	reply.Value = kv.store[args.Key]
	kv.store[args.Key] = kv.store[args.Key] + args.Value

	kv.dupUpdate(args.Clientid, args.Seqid, reply.Value)
}

func StartKVServer() *KVServer {
	kv := new(KVServer)

	// You may need initialization code here.
	kv.store = make(map[string]string)
	kv.duptable = make(map[int64]*dupEntry)
	kv.dupReaders = 0
	kv.clientNum = 0

	return kv
}
