package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"

	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"6.5840/labgob"
	"6.5840/labrpc"
)

// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 3D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 3D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

// A Go object implementing a single Raft peer.
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (3A, 3B, 3C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// Persistent state on all servers
	currentTerm int
	votedFor    int
	log         []LogEntry

	// Volatile state on all servers
	commitIndex int
	lastApplied int
	status      int32 // Leader, candidate, follower
	heartbeat   int32 // Whether received any heartbeat during an election timeout

	// Volatile state on leaders
	nextIndex  []int
	matchIndex []int
}

const (
	LEADER int32 = iota
	CANDIDATE
	FOLLOWER
)

type LogEntry struct {
	Command string
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (3A).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	term = rf.currentTerm
	isleader = (rf.readStatus() == LEADER)

	return term, isleader
}

// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
// before you've implemented snapshots, you should pass nil as the
// second argument to persister.Save().
// after you've implemented snapshots, pass the current snapshot
// (or nil if there's not yet a snapshot).
func (rf *Raft) persist() {
	// Your code here (3C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// raftstate := w.Bytes()
	// rf.persister.Save(raftstate, nil)
}

// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (3C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (3D).

}

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (3A, 3B).
	Term         int
	CandidatedId int
	LastLogIndex int
	LastLogTerm  int
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	// Your data here (3A).
	Term        int
	VoteGranted bool
}

type AppendEntriesArgs struct {
	Term         int // leader's term
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int  // currentTerm, for leader to update itself
	Success bool // true if follower contained entry matching prevLogIndex and prevLogTerm
}

// example RequestVote RPC handler.
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (3A, 3B).
	if rf.killed() {
		return
	}
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.VoteGranted = false

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.setStatus(FOLLOWER)

		reply.VoteGranted = true
		rf.votedFor = args.CandidatedId
	} else if args.Term == rf.currentTerm && rf.votedFor == -1 {
		// Spurious branch, may never enter this
		reply.VoteGranted = true
		rf.votedFor = args.CandidatedId
	}
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if rf.killed() {
		return
	}

	rf.mu.Lock()
	reply.Term = rf.currentTerm

	if rf.currentTerm > args.Term {
		// Stale leader, refuse it
		DPrintf("%v: stale leader's message from term %v, refuse it\n", rf.me, args.Term)
		rf.mu.Unlock()
		return
	}
	rf.currentTerm = args.Term

	atomic.StoreInt32(&rf.heartbeat, 1)
	rf.setStatus(FOLLOWER)
	rf.votedFor = -1

	rf.mu.Unlock()

	// Heartbeat message
	if args.Entries == nil {
		// DPrintf("%v: Receiving heartbeat from leader %v\n", rf.me, args.LeaderId)
		return
	}

	// do something
}

// Leader sends heartbeats periodically in idle time
func (rf *Raft) sendHeartbeat() {
	if rf.killed() || rf.readStatus() != LEADER {
		return
	}
	DPrintf("%v: sending heartbeats to followers\n", rf.me)
	args := &AppendEntriesArgs{}

	rf.mu.Lock()
	args.Term = rf.currentTerm
	args.LeaderId = rf.me
	args.Entries = nil
	rf.mu.Unlock()

	nPeer := len(rf.peers) - 1
	replyBuf := make(chan *AppendEntriesReply, nPeer)

	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		go func(peer int) {
			reply := &AppendEntriesReply{}
			rf.sendAppendEntries(peer, args, reply)
			replyBuf <- reply
			// DPrintf("%v: receive follower %v's response\n", rf.me, peer)
		}(peer)
	}

	for nPeer > 0 && !rf.killed() {
		reply := <-replyBuf
		nPeer--

		if rf.killed() || rf.readStatus() != LEADER {
			return
		}

		rf.mu.Lock()
		if reply.Term > rf.currentTerm {
			rf.setStatus(FOLLOWER)
			rf.votedFor = -1
			rf.currentTerm = reply.Term
			DPrintf("%v: found newer node of term %v, fall back to follower\n", rf.me, reply.Term)
			defer rf.mu.Unlock()
			return
		}
		rf.mu.Unlock()
	}

}

// Start an election
func (rf *Raft) startElection() {

	args := &RequestVoteArgs{}

	rf.mu.Lock()

	rf.currentTerm++
	args.CandidatedId = rf.me
	args.Term = rf.currentTerm

	rf.setStatus(CANDIDATE)
	rf.votedFor = rf.me

	rf.mu.Unlock()

	DPrintf("%v: starting an election for term %v\n", rf.me, args.Term)

	nPeer := len(rf.peers) - 1
	votes := 1

	replyBuf := make(chan *RequestVoteReply, nPeer)

	// Send RequestVote RPC to each peer
	for peer := range rf.peers {
		if peer == rf.me {
			continue
		}
		go func(peer int) {
			reply := &RequestVoteReply{}
			rf.sendRequestVote(peer, args, reply)
			replyBuf <- reply
			if reply.VoteGranted {
				DPrintf("%v: get a vote from %v\n", rf.me, peer)
			}
		}(peer)
	}

	// Receive replies from each peer
	for nPeer > 0 && !rf.killed() {
		reply := <-replyBuf // Block for reply
		nPeer--
		if rf.killed() {
			return
		}

		rf.mu.Lock()
		// May receive a new leader's heartbeat already,
		// or fall back to follower and voted to a newer candidate
		if rf.readStatus() != CANDIDATE || rf.currentTerm != args.Term {
			// DPrintf("%v: states changed, stop election of term %v\n", rf.me, args.Term)
			defer rf.mu.Unlock()
			return
		}

		if reply.Term > rf.currentTerm {
			// Fall back to follower immediately
			DPrintf("%v: found newer node of term %v, stop election of term %v\n", rf.me, reply.Term, args.Term)
			defer rf.mu.Unlock()
			// Spurious: whether the term of replier valid? Need safety check
			rf.setStatus(FOLLOWER)
			rf.votedFor = -1
			return

		} else if reply.VoteGranted {
			votes++
		}

		// Got majority, win election
		if votes*2 > len(rf.peers) {
			DPrintf("%v: win the election, became term %v leader\n", rf.me, rf.currentTerm)
			rf.setStatus(LEADER)
			rf.votedFor = -1

			rf.mu.Unlock()

			// rf.sendHeartbeat()
			// Send heartbeat periodically
			go func() {
				ms := 100
				// time.Sleep(time.Duration(ms) * time.Millisecond)
				for rf.readStatus() == LEADER {
					go rf.sendHeartbeat()
					time.Sleep(time.Duration(ms) * time.Millisecond)
				}
			}()

			return
		}
		rf.mu.Unlock()
	}
	// Lose the election, fall back to follower
	DPrintf("%v: Fail the election of term %v, got %v votes\n", rf.me, args.Term, votes)
	rf.mu.Lock()
	defer rf.mu.Unlock()
	rf.setStatus(FOLLOWER)
	rf.votedFor = -1
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (3B).

	return index, term, isLeader
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	DPrintf("%v: killed by tester\n", rf.me)
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) setStatus(state int32) {
	atomic.StoreInt32(&rf.status, state)
}

func (rf *Raft) readStatus() int32 {
	s := atomic.LoadInt32(&rf.status)
	return s
}

func (rf *Raft) ticker() {
	for !rf.killed() {

		// Your code here (3A)
		// Check if a leader election should be started.

		// pause for a random amount of time between 50 and 350
		// milliseconds.
		ms := 300 + (rand.Int63() % 300)
		DPrintf("%v: new timeout %v ms\n", rf.me, ms)
		time.Sleep(time.Duration(ms) * time.Millisecond)

		if rf.killed() || rf.readStatus() == LEADER {
			continue
		}
		rf.mu.Lock()
		rf.votedFor = -1
		rf.mu.Unlock()
		// No heartbeat received during election timeout, begin an election
		// Xiang jing piao
		if atomic.LoadInt32(&rf.heartbeat) == 0 {
			DPrintf("%v: no heartbeat\n", rf.me)
			go rf.startElection()
		} else {
			DPrintf("%v: heartbeat received\n", rf.me)
			atomic.StoreInt32(&rf.heartbeat, 0)
			rf.mu.Lock()
			rf.votedFor = -1
			rf.mu.Unlock()
		}
	}
}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (3A, 3B, 3C).
	rf.status = FOLLOWER
	rf.currentTerm = 0 //initialized to 0 on first boot, increases monotonically
	rf.votedFor = -1   //candidateId that received vote in current term
	rf.log = make([]LogEntry, 0)
	rf.heartbeat = 0

	rf.commitIndex = 0
	rf.lastApplied = 0

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.ticker()

	return rf
}
