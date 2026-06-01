package agent

import (
	"fmt"
	"strings"
	"unicode"
)

func (a *AgentRuntime) validateOutgoingMessage(target chatReplyTarget, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.sentChatHistory) - 1; i >= 0; i-- {
		previous := a.sentChatHistory[i]
		if targetKey(previous.Target) != targetKey(target) {
			continue
		}
		if messagesTooSimilar(previous.Message, message) {
			return fmt.Errorf("message is too similar to a recent reply; wait for a new conversational opening")
		}
	}
	return nil
}

func messagesTooSimilar(left, right string) bool {
	left = normalizeChatText(left)
	right = normalizeChatText(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if len([]rune(left)) >= 8 && len([]rune(right)) >= 8 &&
		(strings.Contains(left, right) || strings.Contains(right, left)) {
		return true
	}
	leftPairs := runePairs(left)
	rightPairs := runePairs(right)
	if len(leftPairs) == 0 || len(rightPairs) == 0 {
		return false
	}
	common := 0
	for pair := range leftPairs {
		if rightPairs[pair] {
			common++
		}
	}
	denominator := len(leftPairs)
	if len(rightPairs) > denominator {
		denominator = len(rightPairs)
	}
	return float64(common)/float64(denominator) >= 0.72
}

func normalizeChatText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func runePairs(value string) map[string]bool {
	runes := []rune(value)
	out := map[string]bool{}
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = true
	}
	return out
}
