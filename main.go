package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"

	"github.com/zapier/kubechecks/cmd"
)

func main() {
	log.Println("Enabling pprof for profiling")
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	cmd.Execute()
}
