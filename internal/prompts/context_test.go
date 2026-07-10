package prompts

import (
	"strings"
	"testing"
	"time"
)

func TestQQMessageRoutedAtIncludesReplyRouteAndTime(t *testing.T) {
	eventTime := time.Date(2026, 6, 10, 12, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	rendered := QQMessageRoutedAt("group", "1001", "alice", "2001", "hello", eventTime)
	for _, expected := range []string{
		`target_type="group"`,
		`target_id="1001"`,
		`time="2026-06-10T12:30:00+08:00"`,
		"alice (2001):",
		"hello",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("routed QQ message missing %q:\n%s", expected, rendered)
		}
	}
}

func TestQQSelfMessageRoutedAtMarksSelf(t *testing.T) {
	eventTime := time.Date(2026, 7, 3, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	rendered := QQSelfMessageRoutedAt("private", "461105039", "帕秋莉", "180920020", "刚刚那句是我说的", eventTime)
	for _, expected := range []string{
		`<qq_message self="true"`,
		`target_type="private"`,
		`target_id="461105039"`,
		`time="2026-07-03T09:00:00+08:00"`,
		"帕秋莉 (180920020):",
		"刚刚那句是我说的",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("self QQ message missing %q:\n%s", expected, rendered)
		}
	}
}

func TestRootContextSummaryReminderPreservesCurrentTaskSnapshot(t *testing.T) {
	rendered := RootContextSummaryReminder()
	for _, expected := range []string{
		"## 当前任务现场",
		"已经做到哪一步",
		"下一步",
		"工具",
		"准确路径",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("context summary reminder missing %q:\n%s", expected, rendered)
		}
	}
}
