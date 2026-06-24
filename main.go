package main

import (
	rootagent "QqBot/internal/agent"
	authruntime "QqBot/internal/capabilities/auth"
	"QqBot/internal/config"
	"QqBot/internal/db"
	"QqBot/internal/llm"
	"QqBot/internal/metric"
	"QqBot/internal/napcat"
	"QqBot/internal/news"
	"QqBot/internal/ops"
	"QqBot/internal/scheduler"
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
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := db.OpenStore("data/QqBot-store.sqlite")
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	events := rootagent.NewEventQueue()
	llmClient := llm.NewLLMClient(cfg, store)
	authRuntime := authruntime.NewRuntime(cfg, store)
	napcatGateway := napcat.NewNapcatGateway(cfg, store, events, llmClient)
	agentRuntime := rootagent.NewAgentRuntime(cfg, store, events, llmClient, napcatGateway)
	metrics := metric.NewMetricService(store)
	charts := metric.NewMetricChartService(store, metrics)
	ithomePoller := news.NewIthomePoller(cfg, store, events)
	taskScheduler := scheduler.New(20)
	if pollInterval := ithomePoller.PollInterval(); pollInterval > 0 {
		if err := taskScheduler.Register(scheduler.ScheduledTask{
			Name:     "news-poll:ithome",
			Schedule: scheduler.ScheduleSpec{Kind: scheduler.ScheduleInterval, IntervalMs: int(pollInterval.Milliseconds())},
			Run: func(ctx context.Context) (scheduler.TaskRunMetadata, error) {
				ingested, err := ithomePoller.RunOnce(ctx)
				return scheduler.TaskRunMetadata{"ingestedArticles": ingested}, err
			},
		}); err != nil {
			log.Fatalf("register scheduler task: %v", err)
		}
	} else {
		store.Log("info", "IThome polling disabled", map[string]any{"event": "news.ithome.polling_disabled"})
	}
	if err := scheduler.RegisterDataRetentionTasks(taskScheduler, store, metrics); err != nil {
		log.Fatalf("register retention tasks: %v", err)
	}
	if err := scheduler.RegisterAuthTasks(taskScheduler, authRuntime); err != nil {
		log.Fatalf("register auth tasks: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := napcatGateway.Start(ctx); err != nil {
		store.Log("warn", "NapCat gateway start failed; backend continues", map[string]any{"error": err.Error()})
	}
	taskScheduler.Start(ctx)
	agentRuntime.Start(ctx)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           ops.NewHTTPServer(cfg, store, llmClient, napcatGateway, agentRuntime, charts, taskScheduler, authRuntime),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = taskScheduler.Stop(shutdownCtx)
		napcatGateway.Stop()
		store.Log("info", "Server stopped", map[string]any{"event": "server.stopped"})
	}()

	store.Log("info", "Server started", map[string]any{
		"event":          "server.started",
		"port":           cfg.Server.Port,
		"listenGroupIds": cfg.Server.Napcat.ListenGroupIDs,
	})

	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}
