package super_http

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultTimeOut = time.Minute

type StopMode int

const (
	_ StopMode = iota
	StopModeWaitTillFinish
	StopModeWaitTillTimeout
	StopModeForceQuit
)

type StopRes int

const (
	NotYetStopped StopRes = iota
	GracefullyStopped
	RudelyStopped
)

const (
	envFdKey   = "GONEXT_LISTENFD"
	envPPidKey = "GONEXT_PARENT_PID"
)

type Http struct {
	s          *http.Server
	l          net.Listener
	reqTimeout time.Duration

	stateChan  chan connChange
	clientList map[net.Conn]http.ConnState

	serveErr   chan error
	stopOnce   sync.Once
	stopping   bool
	stopResult StopRes
	// 1:normal stop wait till finish;
	// 2:normal stop wait till finish or timeout
	// 3:force stop immediately
	stopTriggerChan chan StopMode

	// 1: stopped gracefully
	// 2: stopped with some clients/requests being forced to close
	stopStatusChan chan StopRes

	serverDone chan bool

	//Zero downtime binary upgrade
	inheritOnce sync.Once
}

type connChange struct {
	conn  net.Conn
	state http.ConnState
}

var pid = os.Getpid()
var ppid = os.Getppid()

func NewHttp(addr string, handler http.Handler, timeout time.Duration) (*Http, error) {
	addr = strings.Trim(addr, " ")
	if addr == "" || handler == nil {
		return nil, errors.New("Param Error")
	}
	if timeout == 0 {
		timeout = defaultTimeOut
	}
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	rtn := Http{
		s:               server,
		reqTimeout:      timeout,
		stateChan:       make(chan connChange),
		clientList:      make(map[net.Conn]http.ConnState),
		serveErr:        make(chan error, 1),
		stopTriggerChan: make(chan StopMode),
		stopStatusChan:  make(chan StopRes),
		stopping:        false,
		stopResult:      NotYetStopped,
		serverDone:      make(chan bool),
	}
	rtn.s.ConnState = rtn.connState
	return &rtn, nil
}

func (h *Http) ListenAndServe() error {
	isInherited := false
	listener, _ := h.tryInheritListener()
	server_addr, _ := net.ResolveTCPAddr("tcp", h.s.Addr)
	if listener != nil && isAddrEqual(listener.Addr(), server_addr) {
		h.l = listener
		isInherited = true
	} else {
		l, err := net.Listen("tcp", h.s.Addr)
		if err != nil {
			return err
		}
		h.l = l
	}
	if isInherited {
		log.Printf("Upgraded Server from [%d] to [%d]\n", ppid, pid)
		syscall.Kill(os.Getppid(), syscall.SIGINT)
	}
	go h.signalHandling()
	go func() {
		h.serveErr <- h.s.Serve(h.l)
		close(h.serveErr)
	}()
	go h.monitor()
	return nil
}
func (h *Http) Wait() {
	err := <-h.serveErr
	if !isUseOfClosedError(err) {
		close(h.serveErr)
	}
	<-h.serverDone
}
func (h *Http) Stop(mode StopMode) StopRes {
	var stopResult StopRes
	h.stopOnce.Do(func() {
		h.s.SetKeepAlivesEnabled(false)
		h.l.Close()

		h.stopTriggerChan <- mode
		log.Printf("[%d]Stopping...\n", pid)
		select {
		case res := <-h.stopStatusChan:
			stopResult = res
		case <-time.After(h.reqTimeout):
			log.Printf("[%d]Timeout reach!\n", pid)
			h.stopTriggerChan <- StopModeForceQuit
			select {
			case res := <-h.stopStatusChan:
				stopResult = res
			case <-time.After(defaultTimeOut):
				panic("Failed to kill")
			}
		}
		h.serverDone <- true
	})
	return stopResult
}
func (h *Http) ChangeBinary(newBinPath string) (int, error) {
	if newBinPath == "" {
		newBinPath = os.Args[0]
	}
	binPath, err := exec.LookPath(newBinPath)
	if err != nil {
		return 0, err
	}
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, envFdKey) && !strings.HasPrefix(v, envPPidKey) {
			env = append(env, v)
		}
	}
	file, err := h.l.(*net.TCPListener).File()
	if err != nil {
		return 0, err
	}
	fd := file.Fd()
	noCloseOnExec(fd)
	env = append(env, fmt.Sprintf("%s=%d", envFdKey, int(fd)))
	env = append(env, fmt.Sprintf("%s=%d", envPPidKey, os.Getpid()))
	workingDir, _ := os.Getwd()
	process, err := os.StartProcess(binPath, os.Args, &os.ProcAttr{
		Dir:   workingDir,
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr, file},
	})
	if err != nil {
		return 0, err
	}
	return process.Pid, nil
}

func (h *Http) signalHandling() {
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR2)
	for {
		s := <-signalsChan
		switch s {
		case syscall.SIGINT:
			log.Printf("Start gracefully stopping the server[%d]\n", pid)
			signal.Stop(signalsChan)
			res := h.Stop(StopModeWaitTillTimeout)
			log.Println("Result->", res)
		case syscall.SIGTERM:
			log.Printf("Start rudely stopping the server [%d]\n", pid)
			signal.Stop(signalsChan)
			h.Stop(StopModeForceQuit)
		case syscall.SIGUSR2:
			log.Println("--->Upgrade Server<-----")
			if _, err := h.ChangeBinary(""); err != nil {
				log.Println("Falied to upgrade:", err.Error())
			}
		}
	}
}

func (h *Http) connState(c net.Conn, cs http.ConnState) {
	h.stateChan <- connChange{c, cs}
}

func (h *Http) monitor() {
	defer func() {
		close(h.stateChan)
		close(h.stopTriggerChan)
		close(h.stopStatusChan)
	}()
	for {
		select {
		case changed := <-h.stateChan:
			h.clientList[changed.conn] = changed.state
			switch changed.state {
			case http.StateIdle:
				if h.stopping {
					changed.conn.Close()
				}
			case http.StateHijacked, http.StateClosed:
				delete(h.clientList, changed.conn)
				if h.stopping && len(h.clientList) == 0 {
					log.Println("Stop DONE")
					h.stopStatusChan <- h.stopResult
					return
				}
			}
		case triggerStop := <-h.stopTriggerChan:
			log.Println("[Monitor] Start Stopping")
			h.stopping = true
			if len(h.clientList) == 0 {
				h.stopStatusChan <- GracefullyStopped
				return
			}
			switch triggerStop {
			case StopModeWaitTillTimeout, StopModeWaitTillFinish:
				log.Println("Gracefully stop triggered")
				h.stopResult = GracefullyStopped
				for conn, state := range h.clientList {
					if state == http.StateIdle {
						log.Println("Closing:", conn.RemoteAddr())
						conn.Close() //will change the state to http.StateClosed
					}
				}
			case StopModeForceQuit:
				log.Println("Force stop triggered")
				h.stopResult = RudelyStopped
				for conn := range h.clientList {
					log.Println("Closing...", conn.RemoteAddr())
					conn.Close()
				}
				h.stopStatusChan <- h.stopResult

			default:
				panic("Illegal Stop Mode!")
			}
		}
	}
}

func (h *Http) tryInheritListener() (net.Listener, error) {
	var retListener net.Listener = nil
	var retError error = nil
	h.inheritOnce.Do(func() {
		strEnvFd := os.Getenv(envFdKey)
		if strEnvFd == "" {
			return
		}
		strParentPid := os.Getenv(envPPidKey)
		parentPid, err := strconv.Atoi(strParentPid)
		if err != nil {
			retError = errors.New("Invalid parent pid value in env")
			return
		}
		if parentPid != os.Getppid() {
			return
		}
		envFd, err := strconv.Atoi(strEnvFd)
		if err != nil {
			retError = errors.New("Invalid fd value in env")
			return
		}
		file := os.NewFile(uintptr(envFd), "listen socket")
		l, err := net.FileListener(file)
		if err != nil {
			retError = errors.New("Failed to bind listener from file")
			file.Close()
			return
		}
		if err := file.Close(); err != nil {
			retError = errors.New("Failed to close socket fd file")
			return
		}
		retListener = l
	})
	return retListener, retError
}
func isAddrEqual(addr1 net.Addr, addr2 net.Addr) bool {
	//return false
	if addr1.Network() != addr2.Network() {
		return false
	}
	return addr1.String() == addr2.String()
}

func isUseOfClosedError(err error) bool {
	if err == nil {
		return false
	}
	if opErr, ok := err.(*net.OpError); ok {
		err = opErr.Err
	}
	return err.Error() == "use of closed network connection"
}

func fcntl(fd int, cmd int, arg int) (val int, err error) {
	r0, _, e1 := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg))
	val = int(r0)
	if e1 != 0 {
		err = e1
	}
	return
}

func noCloseOnExec(fd uintptr) {
	fcntl(int(fd), syscall.F_SETFD, ^syscall.FD_CLOEXEC)
}
