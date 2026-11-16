package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"rockpi-scan/internal/config"
	"rockpi-scan/internal/control"
	"rockpi-scan/internal/web"
)

func main() {
	cfgPath := flag.String("config", "config.json", "путь к файлу конфигурации")
	flag.Parse()

	mgr, err := config.NewManager(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctrl := control.New(mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go ctrl.Run(ctx)

	srv := web.New(mgr, ctrl)

	go func() {
		cfg := mgr.Get()
		log.Printf("web-сервер: http://%s", cfg.Link.HTTPAddr)
		if err := srv.ListenAndServe(cfg.Link.HTTPAddr); err != nil {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("остановка...")
	cancel()
	ctrl.Close()
}