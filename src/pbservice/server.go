package pbservice

import "net"
import "fmt"
import "net/rpc"
import "log"
import "time"
import "viewservice"
import "os"
import "syscall"
import "math/rand"
import "sync"

import "strconv"

// Debugging
const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
  if Debug > 0 {
    n, err = fmt.Printf(format, a...)
  }
  return
}

type PBServer struct {
  l net.Listener
  dead bool // for testing
  unreliable bool // for testing
  me string
  vs *viewservice.Clerk
  done sync.WaitGroup
  finish chan interface{}
  // Your declarations here.
  data map[string]string
  //Do I need to pass this along?
  succesfulOps map[int64]string
  ServerView viewservice.View
  pushDone sync.WaitGroup
  mu sync.Mutex
  init bool
  // sync map[int64]bool
}

func (pb *PBServer) Put(args *PutArgs, reply *PutReply) error {
	// Your code here.
	time.Sleep(6 * time.Millisecond)
	pb.mu.Lock()
	pb.pushDone.Wait()	
	pb.pushDone.Add(1)	
	// if pb.init == false{
	// 	pb.mu.Unlock()
	// 	pb.pushDone.Done()
	// 	reply.Err = "PutError"	
	// 	return nil
	// }
	ans, ok := pb.succesfulOps[args.Nrand]
	if ok{
		reply.PreviousValue = ans
		pb.pushDone.Done()
		pb.mu.Unlock()
		return nil
	}
	var OldValue string
	if args.DoHash == true{
		OldValue = pb.data[args.Key]
		args.Value = strconv.Itoa(int(hash(OldValue+args.Value)))
	}
	pb.data[args.Key] = args.Value
	pb.succesfulOps[args.Nrand] = OldValue
	reply.PreviousValue = OldValue
	primary := pb.ServerView.Primary 
	backup := pb.ServerView.Backup
	pb.mu.Unlock()

    if backup == ""{
		pb.pushDone.Done()
		return nil
	}

	if primary == pb.me  {
		args.DoHash = false
		var reply PutReply
		for ok:=false;ok==false;{
			backup = pb.ServerView.Backup
			if(backup == ""){
				pb.pushDone.Done()
				return nil
			}
			ok = call(backup, "PBServer.Put", args, &reply)
		}
		pb.pushDone.Done()
		return nil
	}else if backup == pb.me{
		pb.pushDone.Done()
		return nil
	}else{
		reply.Err = "PutError"	
		pb.pushDone.Done()
		return nil 
	}
	pb.pushDone.Done()
	return nil
}

func (pb *PBServer) Get(args *GetArgs, reply *GetReply) error {
	// Your code here.
	pb.mu.Lock()
	if pb.ServerView.Primary == pb.me{
		reply.Value = pb.data[args.Key]
	}else{
		reply.Err =Err(string(pb.me)+ "Not the primary")
	}
	pb.mu.Unlock()
	return nil
}

func (pb *PBServer) InitNewBackup(primaryData *SyncDB,reply *SyncDBreply) error {
  // Your code here.
  pb.mu.Lock()
  pb.data = primaryData.StoredValues
  pb.succesfulOps = primaryData.SuccesfulOps
  reply.Value = "ok"
  pb.init = true
  // fmt.Println("INIT here")
  pb.mu.Unlock()
  return nil
}

// ping the viewserver periodically.
func (pb *PBServer) tick() {
  // Your code here.
  v,_ := pb.vs.Ping(pb.ServerView.Viewnum)	
  if v.Primary == pb.me && pb.ServerView.Viewnum == 0{
	  pb.init = true
  }
  if v.Primary!=pb.me && v.Backup!=pb.me{
	pb.init = false
  }
  if v.Primary == pb.me && v.Backup != pb.ServerView.Backup && v.Backup!=""{
	  var reply SyncDBreply
	  var args SyncDB
	  pb.mu.Lock()
	  for ok:=false;ok==false;{
		  args.StoredValues = pb.data
		  args.SuccesfulOps = pb.succesfulOps
		  ok = call(v.Backup,"PBServer.InitNewBackup",args,&reply)
		  if(pb.ServerView.Backup == ""){
			break
		  }
	  }
	  pb.mu.Unlock()
  }
  pb.ServerView = v
}


// tell the server to shut itself down.
// please do not change this function.
func (pb *PBServer) kill() {
  pb.dead = true
  pb.l.Close()
}


func StartServer(vshost string, me string) *PBServer {
  pb := new(PBServer)
  pb.me = me
  pb.vs = viewservice.MakeClerk(me, vshost)
  pb.finish = make(chan interface{})
  // Your pb.* initializations here.

  pb.ServerView.Viewnum = 0
  pb.ServerView.Primary = ""
  pb.ServerView.Backup = ""
  pb.data = make(map[string]string)
  pb.succesfulOps = make(map[int64]string)
  pb.init = false

  rpcs := rpc.NewServer()
  rpcs.Register(pb)

  os.Remove(pb.me)
  l, e := net.Listen("unix", pb.me);
  if e != nil {
    log.Fatal("listen error: ", e);
  }
  pb.l = l

  // please do not change any of the following code,
  // or do anything to subvert it.

  go func() {
    for pb.dead == false {
      conn, err := pb.l.Accept()
      if err == nil && pb.dead == false {
        if pb.unreliable && (rand.Int63() % 1000) < 100 {
          // discard the request.
          conn.Close()
        } else if pb.unreliable && (rand.Int63() % 1000) < 200 {
          // process the request but force discard of reply.
          c1 := conn.(*net.UnixConn)
          f, _ := c1.File()
          err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
          if err != nil {
            fmt.Printf("shutdown: %v\n", err)
          }
          pb.done.Add(1)
          go func() {
            rpcs.ServeConn(conn)
            pb.done.Done()
          }()
        } else {
          pb.done.Add(1)
          go func() {
            rpcs.ServeConn(conn)
            pb.done.Done()
          }()
        }
      } else if err == nil {
        conn.Close()
      }
      if err != nil && pb.dead == false {
        fmt.Printf("PBServer(%v) accept: %v\n", me, err.Error())
        pb.kill()
      }
    }
    DPrintf("%s: wait until all request are done\n", pb.me)
    pb.done.Wait() 
    // If you have an additional thread in your solution, you could
    // have it read to the finish channel to hear when to terminate.
    close(pb.finish)
  }()

  pb.done.Add(1)
  go func() {
    for pb.dead == false {
      pb.tick()
      time.Sleep(viewservice.PingInterval)
    }
    pb.done.Done()
  }()

  return pb
}
