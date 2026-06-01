package agent

import (
	"qqbot-ai/internal/config"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAvailableInvokeToolsAllowsSearchInChats(t *testing.T) {
	session := newRootSession(&config.Config{}, nil, true, nil)
	if tools := session.availableInvokeTools(); len(tools) != 0 {
		t.Fatalf("portal should not expose invoke tools: %#v", tools)
	}

	session.stack = []string{"portal", "qq_group:1"}
	tools := session.availableInvokeTools()
	for _, tool := range []string{"search_web", "search_memory", "send_message"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("group state should allow %s: %#v", tool, tools)
		}
	}
}

func TestNotificationForceFlushBypassesBatchWindow(t *testing.T) {
	session := newRootSession(&config.Config{}, nil, false, nil)
	session.pushNotificationLocked("qq_private:461105039")
	got := session.flushNotificationsIfReady(time.Hour, true)
	if !strings.Contains(got, "qq_private:461105039") {
		t.Fatalf("forced notification flush missing private target: %q", got)
	}
}

func TestAvailableInvokeToolsKeepsStateSpecificTools(t *testing.T) {
	session := newRootSession(&config.Config{}, nil, true, nil)
	tests := []struct {
		state string
		tools []string
	}{
		{state: "ithome", tools: []string{"open_ithome_article"}},
		{state: "terminal", tools: []string{"bash", "read_bash_output"}},
		{state: "zone_out", tools: []string{"zone_out"}},
	}
	for _, test := range tests {
		session.stack = []string{"portal", test.state}
		got := session.availableInvokeTools()
		for _, tool := range test.tools {
			if !slices.Contains(got, tool) {
				t.Fatalf("%s should allow %s: %#v", test.state, tool, got)
			}
		}
	}
}
