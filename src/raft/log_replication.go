package raft

import (
	"fmt"
	"sort"
	"time"
)

var HEARTBEAT_TIME_INTERVAL time.Duration = 100 * time.Millisecond

type AppendEntriesArgs struct {
	Term              int     // leader’s term
	LeaderId          int     // so follower can redirect clients
	PrevLogIndex      int     // index of log entry immediately preceding new ones
	PrevLogTerm       int     // term of prevLogIndex entry
	Entries           []Entry // log entries to store (empty for heartbeat; may send more than one for efficiency)
	LeaderCommitIndex int     // leader’s commitIndex
}

func (args *AppendEntriesArgs) String() string {
	return fmt.Sprintf("{Term: %v, LeaderId: %v, PrevLogIndex: %v, PrevLogTerm: %v, Entries: %v, LeaderCommitIndex:%v }",
		args.Term, args.LeaderId, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries), args.LeaderCommitIndex)
}

type AppendEntriesReply struct {
	Term          int // currentTerm, for leader to update itself
	ConflictTerm  int // term of the conflicting entry
	ConflictIndex int
	Success       bool // true if follower contained entry matching prevLogIndex and prevLogTerm

	// LeaderId int // for debug
}

type InstallSnapshotArgs struct {
	Term              int    // leader’s term
	LeaderId          int    // so follower can redirect clients
	LastIncludedIndex int    // the snapshot replaces all entries up through and including this index
	LastIncludedTerm  int    // term of lastIncludedIndex
	Data              []byte // raw bytes of the snapshot chunk, starting at offset
	// offset int // byte offset where chunk is positioned in the snapshot file
	// done bool  // raw bytes of the snapshot chunk, starting at offset
}

func (args *InstallSnapshotArgs) String() string {
	return fmt.Sprintf("{Term: %v, LeaderId: %v, LastIncludedIndex: %v, LastIncludedTerm: %v, Data: %v }",
		args.Term, args.LeaderId, args.LastIncludedIndex, args.LastIncludedTerm, len(args.Data))
}

type InstallSnapshotReply struct {
	Term int // currentTerm, for leader to update itself
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm
	// reply.LeaderId = args.LeaderId // for debug

	if args.Term < rf.currentTerm {
		Debug(HeartbeatEvent, rf.me, "reject AppendEntries from S%d, for args.term %d < current term %d\n",
			args.LeaderId, args.Term, rf.currentTerm)
		reply.Success = false
		return
	}

	Debug(HeartbeatEvent, rf.me, "recieved AppendEntries from S%d, args=%v\n",
		args.LeaderId, args)

	rf.resetElectionTimer()

	rf.leaderId = args.LeaderId
	if args.Term > rf.currentTerm || rf.role == CANDIDATE {
		rf.stepDown(args.Term)
	}

	// “PrevLogIndex log matched” check
	if args.PrevLogIndex > rf.log.lastIndex() {
		// 跟随者日志过短，返回冲突信息
		Debug(HeartbeatEvent, rf.me, "refuse AppendEntries request from S%d for args.PrevLogIndex %d > rf.lastLogIndex %d \n",
			args.LeaderId, args.PrevLogIndex, rf.log.lastIndex())
		reply.ConflictTerm = rf.log.lastEntry().Term
		reply.ConflictIndex = rf.log.lastIndex()
		reply.Success = false
	} else if args.PrevLogIndex < rf.snapshotIndex {
		// 快照覆盖了 PrevLogIndex，截断日志并追加新条目
		i := rf.snapshotIndex - args.PrevLogIndex - 1
		// log.Printf("---args.PrevLogIndex %d < rf.snapshotIndex %d, i=%d, len(args.Entries)=%d\n", args.PrevLogIndex, rf.snapshotIndex, i, len(args.Entries))
		if len(args.Entries) > i {
			Assert(args.Entries[i].Term == rf.snapshotTerm, "args.Entries[i].Term=%d ,rf.snapshotTerm=%d", args.Entries[i].Term, rf.snapshotTerm)
			oldLogLen := rf.log.length()
			rf.log.place(rf.snapshotIndex, args.Entries[i:]...)
			Debug(HeartbeatEvent, rf.me, "approve AppendEntries request from S%d, old log=%d, new log=%d\n",
				args.LeaderId, oldLogLen, rf.log.length())

			rf.persistSate()
			rf.followerCommit(args.LeaderCommitIndex)
		}
		// j := 0
		// i := rf.snapshotIndex - args.PrevLogIndex - 1
		// if len(args.Entries) > i {
		// 	// it should be "args.Entries[i] == rf.log[0]"
		// 	Assert(args.Entries[i].Term == rf.snapshotTerm, "args.Entries[i].Term=%d ,rf.snapshotTerm=%d", args.Entries[i].Term, rf.snapshotTerm)
		// 	for ; i < len(args.Entries) && j < len(rf.log.entries); i, j = i+1, j+1 {
		// 		rf.log.entries[j] = args.Entries[i]
		// 	}
		// 	rf.log.entries = append(rf.log.entries, args.Entries[i:]...)
		// 	rf.persistSate()
		// }

		reply.Success = true

	} else if rf.log.entry(args.PrevLogIndex).Term != args.PrevLogTerm {
		// 日志条目冲突，回退到冲突任期的最早索引
		reply.ConflictTerm = rf.log.entry(args.PrevLogIndex).Term
		reply.ConflictIndex = args.PrevLogIndex
		Debug(HeartbeatEvent, rf.me, "refuse AppendEntries request from S%d for rf.log[PrevLogIndex %d].Term %d != args.PrevLogTerm %d\n",
			args.LeaderId, args.PrevLogIndex, reply.ConflictTerm, args.PrevLogTerm)
		rf.log.cutOffTail(args.PrevLogIndex)
		rf.persistSate()
		reply.Success = false
	} else {
		// follower's log matches the leader’s log up to and including the args.prevLogIndex
		oldLogLen := rf.log.length()
		rf.log.place(args.PrevLogIndex+1, args.Entries...)
		Debug(HeartbeatEvent, rf.me, "approve AppendEntries request from S%d, old log=%d, new log=%d\n",
			args.LeaderId, oldLogLen, rf.log.length())

		rf.persistSate()
		// follower commit
		rf.followerCommit(args.LeaderCommitIndex)
		reply.Success = true
	}
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Term = rf.currentTerm
	if args.Term < rf.currentTerm {
		Debug(SnapEvent, rf.me, "reject InstallSnapshot from %d, for args.term %d < current term %d\n",
			args.LeaderId, args.Term, rf.currentTerm)
		return
	}

	Debug(SnapEvent, rf.me, "Recieved InstallSnapshot from %d, args=%v\n",
		args.LeaderId, args)

	rf.resetElectionTimer()

	rf.leaderId = args.LeaderId
	if args.Term > rf.currentTerm || rf.role == CANDIDATE {
		rf.stepDown(args.Term)
	}

	if rf.snapshotIndex >= args.LastIncludedIndex || rf.lastApplied >= args.LastIncludedIndex {
		return
	}
	if rf.log.lastIndex() > args.LastIncludedIndex {
		// keep the log entry at snapshotIndex as the first log entry of the sliced log,
		// and the log's length always >= 1
		rf.log.cutOffHead(args.LastIncludedIndex)
	} else {
		// keep the log entry at snapshotIndex as the first log entry of the sliced log,
		// and the log's length always >= 1
		rf.log = Log{[]Entry{{Term: args.LastIncludedTerm}}, args.LastIncludedIndex}
	}
	rf.snapshotIndex = args.LastIncludedIndex
	rf.snapshotTerm = args.LastIncludedTerm
	rf.snapshot = args.Data
	rf.persist()
	rf.applyCond.Broadcast()

}

// ========================== leader ====================================

func (rf *Raft) sendInstallSnapshot(server int, args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	if ok := rf.peers[server].Call("Raft.InstallSnapshot", args, reply); ok {
		rf.mu.Lock()
		defer rf.mu.Unlock()
		Debug(SnapEvent, rf.me, "sendInstallSnapshot: reply from %d, reply.Term=%d, rf.currentTerm=%d", server, reply.Term, rf.currentTerm)
		if args.Term != rf.currentTerm {
			return
		}
		if reply.Term > rf.currentTerm {
			rf.stepDown(reply.Term)
		} else if rf.matchIndex[server] < args.LastIncludedIndex {
			rf.matchIndex[server] = args.LastIncludedIndex
			rf.nextIndex[server] = args.LastIncludedIndex + 1
		}
	}
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if ok := rf.peers[server].Call("Raft.AppendEntries", args, reply); ok {
		rf.mu.Lock()
		defer rf.mu.Unlock()
		if args.Term != rf.currentTerm || rf.role != LEADER {
			return
		}
		Debug(HeartbeatEvent, rf.me, "sendAppendEntries: reply from %d : reply=%+v, request args=%v, rf.currentTerm=%d \n",
			server, reply, args, rf.currentTerm)

		if reply.Success {
			oldNextIndex := rf.nextIndex[server]
			matchIndex := args.PrevLogIndex + len(args.Entries)
			if rf.matchIndex[server] < matchIndex {
				rf.matchIndex[server] = matchIndex
				rf.leaderCommit()
			}
			rf.nextIndex[server] = rf.matchIndex[server] + 1
			Debug(HeartbeatEvent, rf.me, "advance nextIndex %d-->%d\n",
				oldNextIndex, rf.nextIndex[server])
		} else {
			if reply.Term > rf.currentTerm {
				rf.stepDown(reply.Term)
			} else {
				// Assert(reply.LeaderId == rf.me, "reply.LeaderId=%d, rf.me=%d\n", reply.LeaderId, rf.me)
				oldNextIndex := rf.nextIndex[server]
				var newNextIndex int
				if reply.ConflictIndex < rf.snapshotIndex {
					newNextIndex = reply.ConflictIndex + 1
				} else if rf.log.entry(reply.ConflictIndex).Term == reply.ConflictTerm {
					// this situation could occur when "length of follower's log" <= args.prevLogIndex
					newNextIndex = reply.ConflictIndex + 1
				} else {
					// step over all the log with term of rf.log[reply.ConflictIndex]
					term := rf.log.entry(reply.ConflictIndex).Term
					for newNextIndex = reply.ConflictIndex; rf.log.entry(newNextIndex).Term == term && newNextIndex > rf.log.start(); newNextIndex-- {
					}
					newNextIndex += 1
				}

				rf.nextIndex[server] = Max(rf.matchIndex[server]+1, newNextIndex)
				Debug(HeartbeatEvent, rf.me, "roll back nextIndex %d<--%d.\n",
					rf.nextIndex[server], oldNextIndex)
			}
		}
	} else {
		Debug(HeartbeatEvent, rf.me, "sendAppendEntries, no reply from %d. \n", server)
	}
}

func (rf *Raft) leaderLogReplication() {
	Debug(HeartbeatEvent, rf.me, "Leader send heart beats")
	for peerId := 0; peerId < len(rf.peers); peerId++ {
		if peerId == rf.me {
			continue
		}
		prevLogIndex := rf.nextIndex[peerId] - 1
		Assert(rf.matchIndex[peerId] <= prevLogIndex,
			"leaderLogReplication, rf.matchIndex[%d]=%d, prevLogIndex=%d",
			peerId, rf.matchIndex[peerId], prevLogIndex)
		if prevLogIndex < rf.snapshotIndex {
			//sendInstallSnapshot
			args := InstallSnapshotArgs{
				Term:              rf.currentTerm,
				LeaderId:          rf.me,
				LastIncludedIndex: rf.snapshotIndex,
				LastIncludedTerm:  rf.snapshotTerm,
				Data:              rf.snapshot,
			}
			reply := InstallSnapshotReply{}
			go rf.sendInstallSnapshot(peerId, &args, &reply)

			Debug(SnapEvent, rf.me, "Leader send InstallSnapshot to S%d with prevLogIndex=%d, matchIndex=%d, args=%v\n\n",
				peerId, prevLogIndex, rf.matchIndex[peerId], &args)
		} else {
			//sendAppendEntries
			args := AppendEntriesArgs{
				Term:              rf.currentTerm,
				LeaderId:          rf.me,
				PrevLogIndex:      prevLogIndex,
				PrevLogTerm:       rf.log.entry(prevLogIndex).Term,
				Entries:           rf.log.slice(prevLogIndex + 1),
				LeaderCommitIndex: rf.commitIndex,
			}

			reply := AppendEntriesReply{}
			go rf.sendAppendEntries(peerId, &args, &reply)
			Debug(HeartbeatEvent, rf.me, "Leader send AppendEntries to S%d with rf.snapshotIndex=%d, matchIndex=%d, args=%v\n\n",
				peerId, rf.snapshotIndex, rf.matchIndex[peerId], &args)
		}

	}
}

func (rf *Raft) getSortedMatchIndex() []int {
	var matchIndex []int
	matchIndex = append(matchIndex, rf.matchIndex...)
	matchIndex[rf.me] = rf.log.lastIndex()
	sort.Ints(matchIndex)
	return matchIndex
}

func (rf *Raft) leaderCommit() {
	serverMatchedIndex := rf.getSortedMatchIndex()
	//  "(len(rf.matchIndex)+1)/2-1" is the maximum index at which majority of servers matched
	commitIndex := serverMatchedIndex[(len(rf.matchIndex)+1)/2-1]
	if commitIndex <= rf.snapshotIndex {
		return
	}
	// In paper it says that "Raft never commits log entries from previous terms by counting replicas.
	// Only log entries from the leader’s current term are committed by counting replicas" just in case the situation of Figure 8.
	// The situation of Figure 8 can also be avoid if all of the replicas have been agreed at the same index.
	// I add this sencond condition in this code so that it is unnecessary to add another request
	// when the servers were shutdown and then restart to make sure the saved logs were commited and applied
	// || commitIndex == matchIndex[0]
	if (rf.log.entry(commitIndex).Term == rf.currentTerm || commitIndex == serverMatchedIndex[0]) && commitIndex > rf.commitIndex {
		Debug(CommitEvent, rf.me, "commit as leader, %d-%d\n", rf.commitIndex, commitIndex)
		rf.commitIndex = commitIndex
		rf.applyCond.Broadcast()
	}
}

func (rf *Raft) followerCommit(leaderCommitIndex int) {
	if leaderCommitIndex > rf.commitIndex {
		var commitIndex int = Min(leaderCommitIndex, rf.log.lastIndex())
		Debug(CommitEvent, rf.me, "commit as follower, %d-%d\n",
			rf.commitIndex, commitIndex)
		rf.commitIndex = commitIndex
		rf.applyCond.Broadcast()
	}
}

func (rf *Raft) notifyHeartbeat() {
	select {
	case rf.heartbeatNotifyCh <- true:
	default:
	}

}

func (rf *Raft) leaderHeartbeats() {
	for rf.Killed() == false {
		select {
		case _, ok := <-rf.heartbeatNotifyCh:
			if !ok {
				return
			}
		case <-time.After(HEARTBEAT_TIME_INTERVAL):
		}
		rf.mu.Lock()
		if rf.role == LEADER {
			rf.leaderLogReplication()
			rf.mu.Unlock()
		} else {
			rf.mu.Unlock()
			break
		}

		// time.Sleep(HEARTBEAT_TIME_INTERVAL)
	}

}
