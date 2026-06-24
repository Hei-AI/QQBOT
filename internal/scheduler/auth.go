package scheduler

import (
	authruntime "QqBot/internal/capabilities/auth"
	"context"
	"time"
)

func RegisterAuthTasks(s *TaskScheduler, auth *authruntime.Runtime) error {
	if s == nil || auth == nil {
		return nil
	}
	return s.Register(ScheduledTask{
		Name:     "auth-usage-refresh",
		Schedule: ScheduleSpec{Kind: ScheduleCron, Expression: "*/10 * * * *"},
		Run: func(ctx context.Context) (TaskRunMetadata, error) {
			started := time.Now()
			auth.RefreshUsage(ctx)
			return TaskRunMetadata{"refreshed": true, "startedAt": started.UTC().Format(time.RFC3339Nano)}, nil
		},
	})
}
