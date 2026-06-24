package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *Store) AppendAgentStackItem(item AgentStackItem) AgentStackItem {
	if item.RuntimeKey == "" {
		item.RuntimeKey = "root"
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(
		`INSERT INTO agent_stack_items(runtime_key, kind, tool_call_id, created_at, item) VALUES(?, ?, ?, ?, ?)`,
		item.RuntimeKey, item.Kind, item.ToolCallID, formatTime(item.CreatedAt), mustJSON(item),
	)
	if err != nil {
		return item
	}
	if id, idErr := result.LastInsertId(); idErr == nil {
		item.ID = int(id)
		_, _ = s.db.Exec(`UPDATE agent_stack_items SET item = ? WHERE id = ?`, mustJSON(item), id)
	}
	return item
}

func (s *Store) ListAgentStackItems(runtimeKey string, afterID, limit int) []AgentStackItem {
	if runtimeKey == "" {
		runtimeKey = "root"
	}
	query := `SELECT item FROM agent_stack_items WHERE runtime_key = ? AND id > ? ORDER BY id ASC`
	args := []any{runtimeKey, afterID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[AgentStackItem](s.db, query, args...)
}

func (s *Store) BeginToolExecution(item ToolExecutionItem, leaseDuration time.Duration) (ToolExecutionItem, bool, error) {
	if item.ExecutionKey == "" {
		return item, false, errors.New("tool execution key is required")
	}
	if item.RuntimeKey == "" {
		item.RuntimeKey = "root"
	}
	if item.Status == "" {
		item.Status = "processing"
	}
	if item.Attempt <= 0 {
		item.Attempt = 1
	}
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	if leaseDuration <= 0 {
		leaseDuration = 2 * time.Minute
	}
	expiresAt := now.Add(leaseDuration)
	item.LeaseExpiresAt = &expiresAt

	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM tool_executions WHERE execution_key = ?`, item.ExecutionKey).Scan(&raw)
	if err == nil {
		var existing ToolExecutionItem
		if json.Unmarshal([]byte(raw), &existing) != nil {
			return item, false, errors.New("stored tool execution is invalid")
		}
		if existing.Status == "completed" {
			return existing, false, nil
		}
		if existing.Status == "processing" && existing.LeaseExpiresAt != nil && existing.LeaseExpiresAt.After(now) {
			return existing, false, nil
		}
		if existing.SideEffect {
			existing.Status = "uncertain"
			existing.ErrorMessage = "previous side-effecting execution lease expired; automatic replay blocked"
			existing.UpdatedAt = now
			existing.LeaseExpiresAt = nil
			_, _ = s.db.Exec(`UPDATE tool_executions SET status = ?, lease_expires_at = NULL, updated_at = ?, item = ? WHERE execution_key = ?`,
				existing.Status, formatTime(now), mustJSON(existing), existing.ExecutionKey)
			return existing, false, nil
		}
		item.ID = existing.ID
		item.CreatedAt = existing.CreatedAt
		item.Attempt = existing.Attempt + 1
		_, err = s.db.Exec(`UPDATE tool_executions SET status = ?, side_effect = ?, lease_expires_at = ?, updated_at = ?, item = ? WHERE execution_key = ?`,
			item.Status, boolInt(item.SideEffect), formatTime(expiresAt), formatTime(now), mustJSON(item), item.ExecutionKey)
		return item, err == nil, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return item, false, err
	}
	result, err := s.db.Exec(`INSERT INTO tool_executions(execution_key, status, side_effect, lease_expires_at, updated_at, item) VALUES(?, ?, ?, ?, ?, ?)`,
		item.ExecutionKey, item.Status, boolInt(item.SideEffect), formatTime(expiresAt), formatTime(now), mustJSON(item))
	if err != nil {
		return item, false, err
	}
	if id, idErr := result.LastInsertId(); idErr == nil {
		item.ID = int(id)
		_, _ = s.db.Exec(`UPDATE tool_executions SET item = ? WHERE id = ?`, mustJSON(item), id)
	}
	return item, true, nil
}

func (s *Store) CompleteToolExecution(executionKey, resultText string, executionErr error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if s.db.QueryRow(`SELECT item FROM tool_executions WHERE execution_key = ?`, executionKey).Scan(&raw) != nil {
		return
	}
	var item ToolExecutionItem
	if json.Unmarshal([]byte(raw), &item) != nil {
		return
	}
	item.Result = resultText
	item.UpdatedAt = now
	item.LeaseExpiresAt = nil
	if executionErr != nil {
		item.Status = "failed"
		item.ErrorMessage = executionErr.Error()
	} else {
		item.Status = "completed"
		item.ErrorMessage = ""
		item.CompletedAt = &now
	}
	_, _ = s.db.Exec(`UPDATE tool_executions SET status = ?, lease_expires_at = NULL, updated_at = ?, item = ? WHERE execution_key = ?`,
		item.Status, formatTime(now), mustJSON(item), executionKey)
}

func (s *Store) RecoverExpiredToolExecutions(now time.Time) (failed, uncertain int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT execution_key, item FROM tool_executions WHERE status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?`, formatTime(now))
	if err != nil {
		return 0, 0
	}
	defer rows.Close()
	type update struct {
		key  string
		item ToolExecutionItem
	}
	updates := []update{}
	for rows.Next() {
		var key, raw string
		if rows.Scan(&key, &raw) != nil {
			continue
		}
		var item ToolExecutionItem
		if json.Unmarshal([]byte(raw), &item) != nil {
			continue
		}
		item.UpdatedAt = now
		item.LeaseExpiresAt = nil
		if item.SideEffect {
			item.Status = "uncertain"
			item.ErrorMessage = "side-effecting execution lease expired; inspect before replay"
			uncertain++
		} else {
			item.Status = "failed"
			item.ErrorMessage = "execution lease expired"
			failed++
		}
		updates = append(updates, update{key: key, item: item})
	}
	for _, value := range updates {
		_, _ = s.db.Exec(`UPDATE tool_executions SET status = ?, lease_expires_at = NULL, updated_at = ?, item = ? WHERE execution_key = ?`,
			value.item.Status, formatTime(now), mustJSON(value.item), value.key)
	}
	return failed, uncertain
}

func (s *Store) EnqueueAgentTask(item AgentTaskItem) (AgentTaskItem, bool, error) {
	if item.TaskKey == "" || item.TaskType == "" {
		return item, false, errors.New("taskKey and taskType are required")
	}
	now := time.Now()
	if item.Status == "" {
		item.Status = "pending"
	}
	if item.MaxAttempts <= 0 {
		item.MaxAttempts = 3
	}
	if item.AvailableAt.IsZero() {
		item.AvailableAt = now
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`INSERT OR IGNORE INTO agent_tasks(task_key, task_type, status, available_at, lease_expires_at, updated_at, item) VALUES(?, ?, ?, ?, NULL, ?, ?)`,
		item.TaskKey, item.TaskType, item.Status, formatTime(item.AvailableAt), formatTime(now), mustJSON(item))
	if err != nil {
		return item, false, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var raw string
		if err := s.db.QueryRow(`SELECT item FROM agent_tasks WHERE task_key = ?`, item.TaskKey).Scan(&raw); err != nil {
			return item, false, err
		}
		_ = json.Unmarshal([]byte(raw), &item)
		return item, false, nil
	}
	if id, idErr := result.LastInsertId(); idErr == nil {
		item.ID = int(id)
		_, _ = s.db.Exec(`UPDATE agent_tasks SET item = ? WHERE id = ?`, mustJSON(item), id)
	}
	return item, true, nil
}

func (s *Store) ClaimNextAgentTask(workerID string, leaseDuration time.Duration) (AgentTaskItem, bool) {
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}
	now := time.Now()
	expiresAt := now.Add(leaseDuration)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return AgentTaskItem{}, false
	}
	defer tx.Rollback()
	var id int
	var raw string
	err = tx.QueryRow(`SELECT id, item FROM agent_tasks WHERE status = 'pending' AND available_at <= ? ORDER BY id ASC LIMIT 1`, formatTime(now)).Scan(&id, &raw)
	if err != nil {
		return AgentTaskItem{}, false
	}
	var item AgentTaskItem
	if json.Unmarshal([]byte(raw), &item) != nil {
		return AgentTaskItem{}, false
	}
	item.ID = id
	item.Status = "processing"
	item.Attempt++
	item.LeaseOwner = workerID
	item.LeaseExpiresAt = &expiresAt
	item.UpdatedAt = now
	result, err := tx.Exec(`UPDATE agent_tasks SET status = 'processing', lease_expires_at = ?, updated_at = ?, item = ? WHERE id = ? AND status = 'pending'`,
		formatTime(expiresAt), formatTime(now), mustJSON(item), id)
	if err != nil {
		return AgentTaskItem{}, false
	}
	affected, _ := result.RowsAffected()
	if affected != 1 || tx.Commit() != nil {
		return AgentTaskItem{}, false
	}
	return item, true
}

func (s *Store) FinishAgentTask(taskID int, result map[string]any, executionErr error, retryDelay time.Duration) AgentTaskItem {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if s.db.QueryRow(`SELECT item FROM agent_tasks WHERE id = ?`, taskID).Scan(&raw) != nil {
		return AgentTaskItem{}
	}
	var item AgentTaskItem
	if json.Unmarshal([]byte(raw), &item) != nil {
		return AgentTaskItem{}
	}
	item.Result = result
	item.UpdatedAt = now
	item.LeaseOwner = ""
	item.LeaseExpiresAt = nil
	if executionErr == nil {
		item.Status = "completed"
		item.ErrorMessage = ""
		item.CompletedAt = &now
	} else if item.Attempt < item.MaxAttempts {
		item.Status = "pending"
		item.ErrorMessage = executionErr.Error()
		if retryDelay <= 0 {
			retryDelay = time.Minute
		}
		item.AvailableAt = now.Add(retryDelay)
	} else {
		item.Status = "failed"
		item.ErrorMessage = executionErr.Error()
		item.CompletedAt = &now
	}
	_, _ = s.db.Exec(`UPDATE agent_tasks SET status = ?, available_at = ?, lease_expires_at = NULL, updated_at = ?, item = ? WHERE id = ?`,
		item.Status, formatTime(item.AvailableAt), formatTime(now), mustJSON(item), taskID)
	return item
}

func (s *Store) RecoverExpiredAgentTasks(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, item FROM agent_tasks WHERE status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?`, formatTime(now))
	if err != nil {
		return 0
	}
	defer rows.Close()
	items := []AgentTaskItem{}
	for rows.Next() {
		var id int
		var raw string
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var item AgentTaskItem
		if json.Unmarshal([]byte(raw), &item) != nil {
			continue
		}
		item.ID = id
		item.LeaseOwner = ""
		item.LeaseExpiresAt = nil
		item.UpdatedAt = now
		switch {
		case item.SideEffect:
			item.Status = "uncertain"
			item.ErrorMessage = "side-effecting worker lease expired; automatic replay blocked"
		case item.Attempt >= item.MaxAttempts:
			item.Status = "failed"
			item.ErrorMessage = "worker lease expired after maximum attempts"
			item.CompletedAt = &now
		default:
			item.Status = "pending"
			item.AvailableAt = now
			item.ErrorMessage = "worker lease expired; task returned to queue"
		}
		items = append(items, item)
	}
	for _, item := range items {
		_, _ = s.db.Exec(`UPDATE agent_tasks SET status = ?, available_at = ?, lease_expires_at = NULL, updated_at = ?, item = ? WHERE id = ?`,
			item.Status, formatTime(item.AvailableAt), formatTime(now), mustJSON(item), item.ID)
	}
	return len(items)
}

func (s *Store) AgentTaskStatusCounts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM agent_tasks GROUP BY status`)
	if err != nil {
		return map[string]int{}
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if rows.Scan(&status, &count) == nil {
			counts[status] = count
		}
	}
	return counts
}

func (s *Store) ToolExecutionStatusCounts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM tool_executions GROUP BY status`)
	if err != nil {
		return map[string]int{}
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if rows.Scan(&status, &count) == nil {
			counts[status] = count
		}
	}
	return counts
}

func (s *Store) ListToolExecutions() []ToolExecutionItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[ToolExecutionItem](s.db, `SELECT item FROM tool_executions ORDER BY id DESC`)
}

func (s *Store) ListAgentTasks() []AgentTaskItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[AgentTaskItem](s.db, `SELECT item FROM agent_tasks ORDER BY id DESC`)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
