package scheduler

import (
	"QqBot/internal/db"
	"QqBot/internal/metric"
	"context"
	"fmt"
	"runtime"
	"time"
)

const retentionChunkSize = 5000

type retentionSpec struct {
	name          string
	days          int
	offsetMinutes int
}

var retentionSpecs = []retentionSpec{
	{name: "app_log", days: 7, offsetMinutes: 0},
	{name: "llm_chat_call", days: 3, offsetMinutes: 5},
	{name: "metric", days: 7, offsetMinutes: 10},
	{name: "napcat_event", days: 7, offsetMinutes: 15},
	{name: "napcat_qq_message", days: 7, offsetMinutes: 20},
	{name: "terminal_output", days: 7, offsetMinutes: 25},
	{name: "auth_usage_snapshot", days: 30, offsetMinutes: 30},
	{name: "embedding_cache", days: 30, offsetMinutes: 35},
	{name: "oauth_state", days: 7, offsetMinutes: 40},
}

func RegisterDataRetentionTasks(s *TaskScheduler, store *db.Store, metrics *metric.MetricService) error {
	if s == nil || store == nil {
		return nil
	}
	for _, spec := range retentionSpecs {
		spec := spec
		if err := s.Register(ScheduledTask{
			Name:     "data-retention:" + spec.name,
			Schedule: ScheduleSpec{Kind: ScheduleCron, Expression: fmt.Sprintf("%d 0 * * *", spec.offsetMinutes)},
			Run: func(ctx context.Context) (TaskRunMetadata, error) {
				threshold := time.Now().Add(-time.Duration(spec.days) * 24 * time.Hour)
				deleted := 0
				chunks := 0
				aborted := false
				for {
					select {
					case <-ctx.Done():
						aborted = true
					default:
					}
					if aborted {
						break
					}
					n := store.DeleteOlderLimit(spec.name, threshold, retentionChunkSize)
					if n == 0 {
						break
					}
					deleted += n
					chunks++
					runtime.Gosched()
					if n < retentionChunkSize {
						break
					}
				}
				if metrics != nil {
					metrics.Record("scheduler.retention.deleted_rows", float64(deleted), map[string]string{"table": spec.name})
				}
				return TaskRunMetadata{
					"deletedRows":  deleted,
					"chunks":       chunks,
					"thresholdIso": threshold.UTC().Format(time.RFC3339Nano),
					"aborted":      aborted,
				}, nil
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
