package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg, err := LoadConfig("")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := NewStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}

	cache := NewResultCache()
	if err := cache.Load(store.LoadLatestResults()); err != nil {
		log.Fatalf("load cache: %v", err)
	}

	subscriptionService := NewSubscriptionService(store, cfg)
	checkService := NewCheckService(store, cache, cfg)
	if err := subscriptionService.SyncConfiguredSubscriptions(cfg.Subscriptions); err != nil {
		log.Printf("startup subscription sync finished with errors: %v", err)
	}
	if _, err := checkService.RunCheck(nil, nil); err != nil {
		log.Printf("startup check failed: %v", err)
	}

	checkScheduler := NewScheduler(cfg.CheckInterval, func() {
		if _, err := checkService.RunCheck(nil, nil); err != nil {
			log.Printf("scheduled check failed: %v", err)
		}
	})
	refreshScheduler := NewScheduler(cfg.SubscriptionRefreshInterval, func() {
		if err := subscriptionService.SyncConfiguredSubscriptions(cfg.Subscriptions); err != nil {
			log.Printf("scheduled subscription refresh finished with errors: %v", err)
		}
	})

	handler := NewServer(ServerDeps{
		Config:              cfg,
		Store:               store,
		Cache:               cache,
		SubscriptionService: subscriptionService,
		CheckService:        checkService,
		StaticFS:            os.DirFS("dist"),
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	checkScheduler.Start()
	refreshScheduler.Start()
	log.Printf("Node panel listening on http://%s:%d", cfg.Host, cfg.Port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-stop
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		checkScheduler.Stop()
		refreshScheduler.Stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown failed: %v", err)
		}
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}
