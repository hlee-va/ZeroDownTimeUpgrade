package stoppable_http

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
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
}

type connChange struct {
	conn  net.Conn
	state http.ConnState
}

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
	l, err := net.Listen("tcp", h.s.Addr)
	if err != nil {
		return err
	}
	h.l = l

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
		fmt.Println("Stopping...")
		select {
		case res := <-h.stopStatusChan:
			fmt.Println("received", res)
			stopResult = res
		case <-time.After(h.reqTimeout):
			fmt.Println("Timeout reach")
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
					fmt.Println("Stop DONE")
					h.stopStatusChan <- h.stopResult
					return
				}
			}
		case triggerStop := <-h.stopTriggerChan:
			fmt.Println("[Monitor] Start Stopping")
			h.stopping = true
			if len(h.clientList) == 0 {
				h.stopStatusChan <- GracefullyStopped
				return
			}
			switch triggerStop {
			case StopModeWaitTillTimeout, StopModeWaitTillFinish:
				fmt.Println("Gracefully stop triggered")
				h.stopResult = GracefullyStopped
				for conn, state := range h.clientList {
					if state == http.StateIdle {
						fmt.Println("Closing:", conn.RemoteAddr())
						conn.Close() //will change the state to http.StateClosed
					}
				}
			case StopModeForceQuit:
				fmt.Println("Force stop triggered")
				h.stopResult = RudelyStopped
				for conn := range h.clientList {
					fmt.Println("Closing...", conn.RemoteAddr())
					conn.Close()
				}
				h.stopStatusChan <- h.stopResult

			default:
				panic("Illegal Stop Mode!")
			}
		}
	}
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
