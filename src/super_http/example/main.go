package main

import (
	"fmt"
	"github.com/gorilla/mux"
	"net/http"
	"os"
	"time"
	"log"
	"super_http"
)


func buildRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/", Index)
	router.HandleFunc("/task/", Task)
	router.HandleFunc("/newapi/", NewAPI)
	return router
}


func initLog() {
	f, err := os.OpenFile("runtime.log", os.O_APPEND | os.O_CREATE | os.O_RDWR, 0666)
        if err != nil {
            fmt.Printf("error opening file: %v", err)
        }
	log.SetOutput(f)
}

func main() {
	initLog()
	vport := "8181"
	log.Printf("Server launched, pid [%d], parent:[%d]\n", os.Getpid(), os.Getppid())
	log.Printf("Serving at 127.0.0.1:%s\n", vport)
	server, err := super_http.NewHttp(
		"127.0.0.1:" + vport,
		buildRouter(),
		100*time.Second,
	)
	if err != nil {
		log.Printf("Error while creating new server! \n --> %s\n", err.Error())
		os.Exit(-1)
	}
	err = server.ListenAndServe()
	if err != nil {
		log.Printf("Error while launching new server! \n --> %s\n", err.Error())
		os.Exit(-1)
	}
	log.Println("Server started running")
	server.Wait()
	log.Println("Server Stopped!")

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
	log.Printf("New Client Requesting Task:%v\n", r.RemoteAddr)
	log.Printf("Going to do a task that will last %ds using pid [%d]\n",
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
		log.Printf("%d still working...\n", i)
		time.Sleep(time.Second)
	}

	fmt.Fprintf(
		w,
		"Pid [%d]: Job DONE after %ds\n",
		os.Getpid(),
		int(duration.Seconds()),
	)
	log.Printf("Pid [%d]: Job DONE after %ds\n",
		os.Getpid(),
		int(duration.Seconds()))
}
func NewAPI(w http.ResponseWriter, r *http.Request) {

	log.Printf("New Client Requesting NewAPI:%v\n", r.RemoteAddr)
	log.Printf("Serving by pid [%d]\n", os.Getpid())
	fmt.Fprintf(
		w,
		"This is New API serving by %d",
		os.Getpid(),

	)
}




