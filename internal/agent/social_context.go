package agent

import (
	"fmt"
	"qqbot-ai/internal/agentruntime"
	"qqbot-ai/internal/common"
	"sort"
	"strings"
	"time"
)

type socialParticipant struct {
	MessageCount int
	LastSeenAt   time.Time
}

func (a *AgentRuntime) observeChatEvent(event AgentEvent) {
	if event.Type != "napcat_group_message" && event.Type != "napcat_private_message" {
		return
	}
	target := targetFromEvent(event)
	if target.ID == "" {
		return
	}
	userID := strings.TrimSpace(common.AsString(event.Data["userId"]))
	raw := strings.TrimSpace(common.AsString(event.Data["rawMessage"]))
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.socialParticipants == nil {
		a.socialParticipants = map[string]socialParticipant{}
	}
	if userID != "" {
		key := targetKey(target) + ":" + userID
		participant := a.socialParticipants[key]
		participant.MessageCount++
		participant.LastSeenAt = event.At
		a.socialParticipants[key] = participant
	}
	if raw != "" {
		if a.recentTopicsByTarget == nil {
			a.recentTopicsByTarget = map[string][]string{}
		}
		key := targetKey(target)
		a.recentTopicsByTarget[key] = appendLimited(a.recentTopicsByTarget[key], trimPreview(raw, 120), 6)
	}
}

func (a *AgentRuntime) appendSocialContextIfUseful() {
	target, ok := a.session.currentChatTarget()
	if !ok {
		return
	}
	a.mu.Lock()
	key := targetKey(target)
	topics := append([]string(nil), a.recentTopicsByTarget[key]...)
	own := []string{}
	participants := []string{}
	for i := len(a.sentChatHistory) - 1; i >= 0 && len(own) < 3; i-- {
		if targetKey(a.sentChatHistory[i].Target) == key {
			own = append(own, a.sentChatHistory[i].Message)
		}
	}
	for participantKey, participant := range a.socialParticipants {
		if strings.HasPrefix(participantKey, key+":") && time.Since(participant.LastSeenAt) < 30*time.Minute {
			participants = append(participants, fmt.Sprintf("%s (%d recent messages)", strings.TrimPrefix(participantKey, key+":"), participant.MessageCount))
		}
	}
	a.mu.Unlock()
	sort.Strings(participants)
	if len(topics) == 0 && len(own) == 0 {
		return
	}
	lines := []string{
		`<system_reminder kind="social_context">`,
		"current_chat: " + key,
		"instruction: treat this as short-term social awareness. Do not restate it. If you only have another variation of the same joke, wait.",
	}
	if len(own) > 0 {
		lines = append(lines, "recent_own_messages:")
		for _, item := range own {
			lines = append(lines, "- "+item)
		}
	}
	if len(topics) > 0 {
		lines = append(lines, "recent_chat_fragments:")
		for _, item := range topics {
			lines = append(lines, "- "+item)
		}
	}
	if len(participants) > 0 {
		lines = append(lines, "recent_participants:")
		for _, item := range participants {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines, "</system_reminder>")
	a.appendRootContext(RootContextLayerWorkingSet, agentruntime.Message{Role: "user", Content: strings.Join(lines, "\n")})
}
