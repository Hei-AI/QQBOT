package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ScheduleKind string

const (
	ScheduleInterval ScheduleKind = "interval"
	ScheduleCron     ScheduleKind = "cron"
)

type ScheduleSpec struct {
	Kind           ScheduleKind `json:"kind"`
	IntervalMs     int          `json:"intervalMs,omitempty"`
	InitialDelayMs int          `json:"initialDelayMs,omitempty"`
	Expression     string       `json:"expression,omitempty"`
}

type TaskRunMetadata map[string]any

type ScheduledTask struct {
	Name     string
	Schedule ScheduleSpec
	Run      func(context.Context) (TaskRunMetadata, error)
}

type TaskRun struct {
	StartedAt    time.Time       `json:"startedAt"`
	FinishedAt   *time.Time      `json:"finishedAt"`
	DurationMs   *int64          `json:"durationMs"`
	Status       string          `json:"status"`
	ErrorMessage *string         `json:"errorMessage,omitempty"`
	Metadata     TaskRunMetadata `json:"metadata,omitempty"`
}

type TaskStatus struct {
	Name       string       `json:"name"`
	Schedule   ScheduleSpec `json:"schedule"`
	NextRunAt  *time.Time   `json:"nextRunAt"`
	IsRunning  bool         `json:"isRunning"`
	RecentRuns []TaskRun    `json:"recentRuns"`
}

type TaskScheduler struct {
	mu          sync.Mutex
	tasks       map[string]*taskEntry
	started     bool
	stopping    bool
	historySize int
	now         func() time.Time
}

type taskEntry struct {
	task       ScheduledTask
	cancel     context.CancelFunc
	driverDone chan struct{}
	running    bool
	runDone    chan struct{}
	nextRunAt  *time.Time
	recentRuns []TaskRun
}

func New(historySize int) *TaskScheduler {
	if historySize <= 0 {
		historySize = 20
	}
	return &TaskScheduler{tasks: map[string]*taskEntry{}, historySize: historySize, now: time.Now}
}

func (s *TaskScheduler) Register(task ScheduledTask) error {
	if strings.TrimSpace(task.Name) == "" {
		return fmt.Errorf("task name is required")
	}
	if task.Run == nil {
		return fmt.Errorf("task run function is required")
	}
	if err := validateSchedule(task.Schedule); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("cannot register task %q after scheduler has started", task.Name)
	}
	if _, exists := s.tasks[task.Name]; exists {
		return fmt.Errorf("duplicate scheduled task name: %s", task.Name)
	}
	s.tasks[task.Name] = &taskEntry{task: task}
	return nil
}

func (s *TaskScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	entries := make([]*taskEntry, 0, len(s.tasks))
	for _, entry := range s.tasks {
		entries = append(entries, entry)
	}
	s.mu.Unlock()

	for _, entry := range entries {
		driverCtx, cancel := context.WithCancel(ctx)
		entry.cancel = cancel
		entry.driverDone = make(chan struct{})
		go s.runDriver(driverCtx, entry)
	}
}

func (s *TaskScheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	entries := make([]*taskEntry, 0, len(s.tasks))
	for _, entry := range s.tasks {
		entries = append(entries, entry)
		if entry.cancel != nil {
			entry.cancel()
		}
	}
	s.mu.Unlock()

	for _, entry := range entries {
		if entry.driverDone != nil {
			select {
			case <-entry.driverDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		for {
			s.mu.Lock()
			running := entry.running
			done := entry.runDone
			s.mu.Unlock()
			if !running || done == nil {
				break
			}
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

func (s *TaskScheduler) TriggerNow(ctx context.Context, name string) (map[string]any, error) {
	entry, err := s.entry(name)
	if err != nil {
		return nil, err
	}
	started, done := s.fire(ctx, entry, true)
	if !started {
		return map[string]any{"ok": false, "reason": "overlap"}, nil
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return map[string]any{"ok": true}, nil
}

func (s *TaskScheduler) ListStatus() []TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.tasks))
	for name := range s.tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]TaskStatus, 0, len(names))
	for _, name := range names {
		entry := s.tasks[name]
		recent := append([]TaskRun(nil), entry.recentRuns...)
		nextRunAt := cloneTime(entry.nextRunAt)
		out = append(out, TaskStatus{
			Name:       entry.task.Name,
			Schedule:   entry.task.Schedule,
			NextRunAt:  nextRunAt,
			IsRunning:  entry.running,
			RecentRuns: recent,
		})
	}
	return out
}

func (s *TaskScheduler) entry(name string) (*taskEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.tasks[name]
	if entry == nil {
		return nil, fmt.Errorf("unknown scheduled task: %s", name)
	}
	return entry, nil
}

func (s *TaskScheduler) runDriver(ctx context.Context, entry *taskEntry) {
	defer close(entry.driverDone)
	first := true
	for {
		wait, nextRunAt := nextDelay(entry.task.Schedule, s.now(), first)
		if wait < 0 {
			s.setNextRunAt(entry, nil)
			return
		}
		s.setNextRunAt(entry, &nextRunAt)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.setNextRunAt(entry, nil)
			return
		case <-timer.C:
			s.fire(ctx, entry, false)
			first = false
		}
	}
}

func (s *TaskScheduler) fire(ctx context.Context, entry *taskEntry, manual bool) (bool, <-chan struct{}) {
	s.mu.Lock()
	if s.stopping || entry.running {
		if !s.stopping && !manual {
			now := s.now()
			zero := int64(0)
			entry.recentRuns = append(entry.recentRuns, TaskRun{
				StartedAt:  now,
				FinishedAt: &now,
				DurationMs: &zero,
				Status:     "skipped_overlap",
			})
			entry.recentRuns = trimRuns(entry.recentRuns, s.historySize)
		}
		s.mu.Unlock()
		return false, nil
	}
	entry.running = true
	entry.runDone = make(chan struct{})
	done := entry.runDone
	run := TaskRun{StartedAt: s.now(), Status: "running"}
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			entry.running = false
			close(entry.runDone)
			s.mu.Unlock()
		}()
		metadata, err := entry.task.Run(ctx)
		finished := s.now()
		duration := finished.Sub(run.StartedAt).Milliseconds()
		s.mu.Lock()
		defer s.mu.Unlock()
		run.FinishedAt = &finished
		run.DurationMs = &duration
		if metadata != nil {
			run.Metadata = metadata
		}
		if err != nil {
			msg := err.Error()
			run.Status = "error"
			run.ErrorMessage = &msg
		} else {
			run.Status = "success"
		}
		entry.recentRuns = append(entry.recentRuns, run)
		entry.recentRuns = trimRuns(entry.recentRuns, s.historySize)
	}()
	return true, done
}

func trimRuns(runs []TaskRun, capacity int) []TaskRun {
	if len(runs) <= capacity {
		return runs
	}
	return runs[len(runs)-capacity:]
}

func validateSchedule(schedule ScheduleSpec) error {
	switch schedule.Kind {
	case ScheduleInterval:
		if schedule.IntervalMs <= 0 {
			return fmt.Errorf("interval schedule requires positive intervalMs")
		}
		if schedule.InitialDelayMs < 0 {
			return fmt.Errorf("interval schedule requires non-negative initialDelayMs")
		}
	case ScheduleCron:
		if _, err := parseCron(schedule.Expression); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported schedule kind: %s", schedule.Kind)
	}
	return nil
}

func nextDelay(schedule ScheduleSpec, now time.Time, first bool) (time.Duration, time.Time) {
	switch schedule.Kind {
	case ScheduleInterval:
		delayMs := schedule.IntervalMs
		if first {
			delayMs = schedule.InitialDelayMs
		}
		delay := time.Duration(delayMs) * time.Millisecond
		return delay, now.Add(delay)
	case ScheduleCron:
		next, err := nextCronTime(schedule.Expression, now)
		if err != nil {
			return -1, time.Time{}
		}
		return next.Sub(now), next
	default:
		return -1, time.Time{}
	}
}

func (s *TaskScheduler) setNextRunAt(entry *taskEntry, next *time.Time) {
	s.mu.Lock()
	entry.nextRunAt = cloneTime(next)
	s.mu.Unlock()
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := *value
	return &cloned
}

type cronSpec struct {
	minuteEvery int
	minuteAt    *int
	hourEvery   bool
	hourAt      *int
}

func parseCron(expression string) (cronSpec, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return cronSpec{}, fmt.Errorf("cron expression must have 5 fields")
	}
	spec := cronSpec{}
	minute := fields[0]
	switch {
	case minute == "*":
		spec.minuteEvery = 1
	case strings.HasPrefix(minute, "*/"):
		n, err := strconv.Atoi(strings.TrimPrefix(minute, "*/"))
		if err != nil || n <= 0 || n > 59 {
			return cronSpec{}, fmt.Errorf("invalid cron minute interval")
		}
		spec.minuteEvery = n
	default:
		n, err := strconv.Atoi(minute)
		if err != nil || n < 0 || n > 59 {
			return cronSpec{}, fmt.Errorf("invalid cron minute")
		}
		spec.minuteAt = &n
	}

	hour := fields[1]
	if hour == "*" {
		spec.hourEvery = true
	} else {
		n, err := strconv.Atoi(hour)
		if err != nil || n < 0 || n > 23 {
			return cronSpec{}, fmt.Errorf("invalid cron hour")
		}
		spec.hourAt = &n
	}
	for _, field := range fields[2:] {
		if field != "*" {
			return cronSpec{}, errors.New("only daily cron expressions with wildcard day/month/week are supported")
		}
	}
	return spec, nil
}

func nextCronTime(expression string, now time.Time) (time.Time, error) {
	spec, err := parseCron(expression)
	if err != nil {
		return time.Time{}, err
	}
	start := now.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		t := start.Add(time.Duration(i) * time.Minute)
		if spec.hourAt != nil && t.Hour() != *spec.hourAt {
			continue
		}
		if !spec.hourEvery && spec.hourAt == nil {
			continue
		}
		if spec.minuteAt != nil && t.Minute() != *spec.minuteAt {
			continue
		}
		if spec.minuteEvery > 0 && t.Minute()%spec.minuteEvery != 0 {
			continue
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("no cron time found")
}
