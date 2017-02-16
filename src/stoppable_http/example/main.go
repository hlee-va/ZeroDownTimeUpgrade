package main

import (
	"fmt"
	"github.com/gorilla/mux"
	"net/http"
	"os"
	"os/signal"
	"stoppable_http"
	"syscall"
	"time"
)


func buildRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/", Index)
	router.HandleFunc("/task/", Task)
	return router
}
func signalHandling(h *stoppable_http.Http) {
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR2)
	for {
		s := <-signalsChan
		switch s {
		case syscall.SIGINT:
			fmt.Println("Start gracefully stopping the server")
			signal.Stop(signalsChan)
			res := h.Stop(stoppable_http.StopModeWaitTillTimeout)
			fmt.Println("Result->", res)
		case syscall.SIGTERM:
			fmt.Println("Start rudely stopping the server")
			signal.Stop(signalsChan)
			h.Stop(stoppable_http.StopModeForceQuit)
		case syscall.SIGUSR2:
			fmt.Println("Upgrade under construction")
		}
	}
}
func main() {
	vport := "8181"
	fmt.Printf("Server launched, pid [%d]\n", os.Getpid())
	fmt.Println("Serving at 127.0.0.1:",vport)
	server, err := stoppable_http.NewHttp(
		"127.0.0.1:"+vport,
		buildRouter(),
		30*time.Second,
	)
	if err != nil {
		fmt.Printf("Error while creating new server! \n --> %s\n", err.Error())
		os.Exit(-1)
	}
	err = server.ListenAndServe()
	if err != nil {
		fmt.Printf("Error while launching new server! \n --> %s\n", err.Error())
		os.Exit(-1)
	}
	fmt.Println("Server started running")
	go signalHandling(server)
	server.Wait()
	fmt.Println("Server Stopped!")

}

func Index(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(
		w,
		"Hello World!\n Serving by %d",
		os.Getpid(),
	)
}

func Task(w http.ResponseWriter, r *http.Request) {
	duration, err := time.ParseDuration(r.FormValue("duration"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	fmt.Printf("Remote Client:%v\n", r.RemoteAddr)
	fmt.Printf("Going to do a task that will last %ds using pid [%d]\n",
		int(duration.Seconds()),
		os.Getpid())
	fmt.Fprintf(
		w,
		"Going to do a task that will last %ds using pid [%d]\n",
		int(duration.Seconds()),
		os.Getpid(),
	)
	w.(http.Flusher).Flush()
	upper := int(duration.Seconds())
	for i := 0; i != upper; i++ {
		fmt.Printf("%d still working...\n", i)
		fmt.Fprintf(w, "%d still working...\n", i)
		w.(http.Flusher).Flush()
		time.Sleep(time.Second)
	}

	fmt.Fprintf(
		w,
		"Pid [%d]: Job DONE after %ds\n",
		os.Getpid(),
		int(duration.Seconds()),
	)
	fmt.Printf("Pid [%d]: Job DONE after %ds\n",
		os.Getpid(),
		int(duration.Seconds()))
}
