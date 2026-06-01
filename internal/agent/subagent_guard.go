package agent

import (
	"log"
	"time"
)

func (a *AgentRuntime) subAgentAllowed(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	until := a.subAgentOpenUntil[name]
	if until.IsZero() || time.Now().After(until) {
		delete(a.subAgentOpenUntil, name)
		return true
	}
	log.Printf("[AGENT] sub-agent circuit open name=%s remaining=%s", name, time.Until(until))
	return false
}

func (a *AgentRuntime) reportSubAgentResult(name string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.subAgentFailures == nil {
		a.subAgentFailures = map[string]int{}
	}
	if a.subAgentOpenUntil == nil {
		a.subAgentOpenUntil = map[string]time.Time{}
	}
	if err == nil {
		delete(a.subAgentFailures, name)
		delete(a.subAgentOpenUntil, name)
		return
	}
	a.subAgentFailures[name]++
	if a.subAgentFailures[name] >= 3 {
		a.subAgentOpenUntil[name] = time.Now().Add(2 * time.Minute)
		a.subAgentFailures[name] = 0
		log.Printf("[AGENT] sub-agent circuit opened name=%s duration=%s", name, 2*time.Minute)
	}
}
