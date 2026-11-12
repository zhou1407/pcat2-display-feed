package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetFlags(log.Ltime)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "ctl":
			os.Exit(ctlMain(os.Args[2:]))
		case "probe":
			ProbeMCU()
			os.Exit(0)
		}
	}

	socket := flag.String("socket", "/var/run/pcat2d.sock", "control socket path")
	noApply := flag.Bool("noapply", false, "skip device writes at startup (probe/debug)")
	flag.Parse()

	disp, err := OpenDisplay()
	if err != nil {
		log.Fatalf("pcat2d: open display: %v", err)
	}
	defer disp.Close()

	app := NewApp(disp, *socket, *noApply)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		close(app.stop)
	}()

	if err := app.Run(); err != nil {
		log.Fatalf("pcat2d: %v", err)
	}
}
