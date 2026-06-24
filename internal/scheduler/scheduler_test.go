package scheduler

import (
	"QqBot/internal/db"
	"QqBot/internal/metric"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterRejectsDuplicate(t *testing.T) {
	s := New(3)
	task := ScheduledTask{Name: "x", Schedule: ScheduleSpec{Kind: ScheduleInterval, IntervalMs: 1000}, Run: func(context.Context) (TaskRunMetadata, error) {
		return nil, nil
	}}
	if err := s.Register(task); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := s.Register(task); err == nil {
		t.Fatal("expected duplicate register to fail")
	}
}

func TestTriggerNowRecordsSuccessAndFailure(t *testing.T) {
	s := New(3)
	fail := false
	if err := s.Register(ScheduledTask{Name: "x", Schedule: ScheduleSpec{Kind: ScheduleInterval, IntervalMs: 1000}, Run: func(context.Context) (TaskRunMetadata, error) {
		if fail {
			return TaskRunMetadata{"phase": "fail"}, errors.New("boom")
		}
		return TaskRunMetadata{"phase": "ok"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TriggerNow(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	waitRunStatus(t, s, "success")
	fail = true
	if _, err := s.TriggerNow(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	waitRunStatus(t, s, "error")
}

func TestTriggerNowRejectsOverlap(t *testing.T) {
	s := New(3)
	release := make(chan struct{})
	if err := s.Register(ScheduledTask{Name: "x", Schedule: ScheduleSpec{Kind: ScheduleInterval, IntervalMs: 1000}, Run: func(ctx context.Context) (TaskRunMetadata, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = s.TriggerNow(context.Background(), "x")
	}()
	waitRunning(t, s, true)
	resp, err := s.TriggerNow(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if resp["ok"] != false || resp["reason"] != "overlap" {
		t.Fatalf("unexpected overlap response: %#v", resp)
	}
	close(release)
	waitRunStatus(t, s, "success")
}

func TestListStatusIncludesNextRunAtAndIsRunning(t *testing.T) {
	s := New(3)
	if err := s.Register(ScheduledTask{Name: "x", Schedule: ScheduleSpec{Kind: ScheduleInterval, IntervalMs: 1000, InitialDelayMs: 50}, Run: func(context.Context) (TaskRunMetadata, error) {
		return nil, nil
	}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := s.ListStatus()
		if len(status) != 1 {
			t.Fatalf("unexpected status count: %d", len(status))
		}
		if status[0].NextRunAt != nil {
			if status[0].IsRunning {
				t.Fatalf("task should not be running before initial delay: %#v", status[0])
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected nextRunAt to be populated: %#v", s.ListStatus())
}

func TestDataRetentionDeletesInChunks(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old := time.Now().Add(-8 * 24 * time.Hour)
	fresh := time.Now()
	for i := 0; i < retentionChunkSize*2+12; i++ {
		store.Data.AppLogs = append(store.Data.AppLogs, db.AppLogItem{ID: i + 1, CreatedAt: old})
	}
	store.Data.AppLogs = append(store.Data.AppLogs, db.AppLogItem{ID: 999999, CreatedAt: fresh})

	s := New(5)
	if err := RegisterDataRetentionTasks(s, store, metric.NewMetricService(store)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TriggerNow(context.Background(), "data-retention:app_log"); err != nil {
		t.Fatal(err)
	}
	statuses := s.ListStatus()
	var appLog TaskStatus
	for _, status := range statuses {
		if status.Name == "data-retention:app_log" {
			appLog = status
			break
		}
	}
	if len(appLog.RecentRuns) != 1 {
		t.Fatalf("expected app_log retention run, got %#v", statuses)
	}
	meta := appLog.RecentRuns[0].Metadata
	if meta["deletedRows"] != retentionChunkSize*2+12 {
		t.Fatalf("unexpected deletedRows metadata: %#v", meta)
	}
	if meta["chunks"] != 3 {
		t.Fatalf("unexpected chunks metadata: %#v", meta)
	}
	if got := len(store.Snapshot().AppLogs); got != 1 {
		t.Fatalf("expected one fresh log to remain, got %d", got)
	}
}

func waitRunStatus(t *testing.T, s *TaskScheduler, status string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		list := s.ListStatus()
		if len(list) == 1 && len(list[0].RecentRuns) > 0 && list[0].RecentRuns[len(list[0].RecentRuns)-1].Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run status did not become %s: %#v", status, s.ListStatus())
}

func waitRunning(t *testing.T, s *TaskScheduler, running bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		list := s.ListStatus()
		if len(list) == 1 && list[0].IsRunning == running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("running did not become %v: %#v", running, s.ListStatus())
}
