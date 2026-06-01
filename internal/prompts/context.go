package prompts

import (
	"fmt"
	"strings"
	"time"
)

type ArticleSummary struct {
	ID              int
	Title           string
	PublishedAtText string
	URL             string
	RSSSummary      string
}

type PortalTarget struct {
	Label            string
	Kind             string
	HasEntered       bool
	UnreadCount      int
	Summary          string
	EnterCommandText string
}

func QQMessage(nickname, userID, messageBody string) string {
	return fmt.Sprintf(`<qq_message>
%s (%s):
%s
</qq_message>`, nickname, userID, messageBody)
}

func QQMessageAt(nickname, userID, messageBody string, t time.Time) string {
	if t.IsZero() {
		return QQMessage(nickname, userID, messageBody)
	}
	bt := t
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		bt = t.In(loc)
	}
	return fmt.Sprintf(`<qq_message time="%s">
%s (%s):
%s
</qq_message>`, bt.Format("2006-01-02 15:04:05 -07:00"), nickname, userID, messageBody)
}

func SelfQQMessageAt(nickname, userID, messageBody string, t time.Time) string {
	bt := t
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		bt = t.In(loc)
	}
	return fmt.Sprintf(`<qq_message time="%s" self="true">
%s (%s):
%s
</qq_message>`, bt.Format("2006-01-02 15:04:05 -07:00"), nickname, userID, messageBody)
}

func ConversationSummary(summary string) string {
	return fmt.Sprintf(`<conversation_summary>
%s
</conversation_summary>`, summary)
}

func WakeReminder(t time.Time) string {
	bt := beijingTime(t)
	return fmt.Sprintf(`<system_reminder kind="time" time="%s">
current_time: %s
timezone: Asia/Shanghai
</system_reminder>`, bt.Format("2006-01-02 15:04:05 -07:00"), bt.Format("2006-01-02 15:04"))
}

func EnterZoneOutInstruction() string {
	return `<system_instruction>
你已进入神游状态。
现在不能看群消息，也不能直接搜索或发群消息；如果要继续思考，请调用 invoke(tool="zone_out", thought="...")，如果想回到上一级状态，调用 back。
</system_instruction>`
}

func ExitZoneOutInstruction() string {
	return `<system_instruction>
你已结束神游，回到门户状态。
如需进入某个目标，请调用 enter。
</system_instruction>`
}

func WaitResumeReminder(isTimeout, isEvent bool, resumedStateLabel, eventSummary string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<system_reminder kind="wait_resume" time="%s">`+"\n", beijingTime(time.Now()).Format("2006-01-02 15:04:05 -07:00"))
	if isTimeout {
		b.WriteString("reason: timeout\n")
	}
	if isEvent {
		b.WriteString("reason: event\n")
	}
	fmt.Fprintf(&b, "resumed_state: %s\n", resumedStateLabel)
	if strings.TrimSpace(eventSummary) != "" {
		fmt.Fprintf(&b, "event_summary: %s\n", eventSummary)
	}
	b.WriteString("</system_reminder>")
	return b.String()
}

func WebSearchInstruction(question string) string {
	return fmt.Sprintf(`<system_instruction>
你正在继承主 agent 当前上下文，临时执行一次网页检索子任务。
这次不是群聊发言决策，也不是直接回复群消息；本轮唯一目标是为主 agent 搜集信息，并给回一段可复用的中文摘要。
你应该基于当前上下文理解这个问题在指什么，再决定搜索策略，而不是把问题孤立地当成一句无上下文文本。
当前要检索的问题：%s
你可以按需把问题拆成多个关键词或子问题，并多次调用 search_web_raw。
如果信息已经足够，调用 finalize_web_search 输出最终摘要；摘要必须基于检索结果，且在证据不足、结果冲突或时间不明确时明确保留不确定性。
不要直接输出自由文本回答，不要复述思考过程，只通过工具调用推进本轮任务。
</system_instruction>`, question)
}

func ITHomeArticleListInstruction(displayName string, isNewMode bool, hiddenNewCount int, articles []ArticleSummary) string {
	var b strings.Builder
	b.WriteString("<system_instruction>\n")
	fmt.Fprintf(&b, "你已进入 %s 资讯空间。\n", displayName)
	if isNewMode {
		b.WriteString("以下是游标之后最新的一批新文章。\n")
		if hiddenNewCount > 0 {
			fmt.Fprintf(&b, "本轮只展示最新几篇；更早的 %d 篇新文章已随本次进入一起略过。\n", hiddenNewCount)
		}
	} else {
		b.WriteString("以下是最近文章列表。\n")
	}
	b.WriteString("如果想阅读全文，调用 invoke(tool=\"open_ithome_article\", articleId=...)；如果想离开，调用 back。\n")
	b.WriteString("</system_instruction>\n<ithome_article_list>\n")
	for _, article := range articles {
		fmt.Fprintf(&b, "[%d] %s\n发布时间：%s\n链接：%s\n摘要：%s\n\n",
			article.ID, article.Title, article.PublishedAtText, article.URL, article.RSSSummary)
	}
	b.WriteString("</ithome_article_list>")
	return b.String()
}

func ITHomeArticleDetail(title, publishedAtText, url, content string, fallbackToSummary, truncated bool, maxChars int) string {
	var b strings.Builder
	b.WriteString("<system_instruction>\n以下是当前打开的 IT 之家文章。\n")
	if fallbackToSummary {
		b.WriteString("正文暂不可用，以下内容来自 RSS 摘要整理。\n")
	}
	if truncated {
		fmt.Fprintf(&b, "正文过长，以下仅保留前 %d 字。\n", maxChars)
	}
	b.WriteString("看完后可以继续打开别的文章，或者调用 back 离开资讯空间。\n</system_instruction>\n")
	fmt.Fprintf(&b, `<ithome_article>
标题：%s
发布时间：%s
链接：%s

正文：
%s
</ithome_article>`, title, publishedAtText, url, content)
	return b.String()
}

func PortalSnapshot(groups, privates, feeds []PortalTarget) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<system_reminder kind="state" time="%s">`+"\n", beijingTime(time.Now()).Format("2006-01-02 15:04:05 -07:00"))
	b.WriteString("current_state: portal\n")
	b.WriteString("allowed_tools: enter, wait\n")
	b.WriteString("instruction: choose one target to enter, or wait.\n")
	b.WriteString("available_targets:\n")
	for _, group := range groups {
		fmt.Fprintf(&b, "- label: %s\n  type: qq_group\n  unread: %d\n  enter: %s\n", group.Label, group.UnreadCount, group.EnterCommandText)
		if strings.TrimSpace(group.Summary) != "" {
			fmt.Fprintf(&b, "  latest: %s\n", group.Summary)
		}
		if group.UnreadCount > 0 {
			fmt.Fprintf(&b, "  status: unread\n")
		} else if group.HasEntered {
			fmt.Fprintf(&b, "  status: entered\n")
		} else {
			fmt.Fprintf(&b, "  status: not_entered\n")
		}
	}
	for _, private := range privates {
		fmt.Fprintf(&b, "- label: %s\n  type: qq_private\n  unread: %d\n  enter: %s\n", private.Label, private.UnreadCount, private.EnterCommandText)
		if strings.TrimSpace(private.Summary) != "" {
			fmt.Fprintf(&b, "  latest: %s\n", private.Summary)
		}
		if private.UnreadCount > 0 {
			fmt.Fprintf(&b, "  status: unread\n")
		} else if private.HasEntered {
			fmt.Fprintf(&b, "  status: entered\n")
		} else {
			fmt.Fprintf(&b, "  status: not_entered\n")
		}
	}
	for _, feed := range feeds {
		fmt.Fprintf(&b, "- label: %s\n  type: feed\n  kind: %s\n  unread: %d\n  enter: %s\n", feed.Label, feed.Kind, feed.UnreadCount, feed.EnterCommandText)
		if feed.UnreadCount > 0 {
			fmt.Fprintf(&b, "  status: unread\n")
		} else if feed.HasEntered {
			fmt.Fprintf(&b, "  status: entered\n")
		} else {
			fmt.Fprintf(&b, "  status: not_entered\n")
		}
	}
	b.WriteString("- label: 神游\n  type: zone_out\n  enter: enter(id=\"zone_out\")\n  status: available\n")
	b.WriteString("</system_reminder>")
	return b.String()
}

func beijingTime(t time.Time) time.Time {
	if t.IsZero() {
		t = time.Now()
	}
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return t.In(loc)
	}
	return t
}

func RootContextSummaryReminder() string {
	return `<system_reminder kind="context_summary_task">
你现在不是在继续执行动作，而是在为当前 root agent 整理“稍后继续接上”的累计上下文摘要。
这份摘要不是状态面板，也不是任务汇报，而是同一个人中途离开后回来继续延续当下局面的工作记忆。
请优先保留上下文压缩后最容易丢失、但最影响后续自然延续的内容：跨轮仍成立的背景，当前仍在延续的线索，关键对象，帕秋莉自己的感觉与倾向，已经做过的关键动作及结果，以及后续还可以继续展开的点。
摘要使用 Markdown 二级标题，按固定顺序组织为：## 持续背景、## 仍在延续的线索、## 关键对象、## 帕秋莉这边的感觉与倾向、## 已做动作与结果、## 还可以继续展开的点。
不要直接输出自由文本回复，必须调用 summary 工具；summary 参数应是简洁但信息完整的中文字符串。
</system_reminder>`
}

func StoryContextSummaryReminder() string {
	return `<system_reminder kind="story_summary_task">
你现在不是在创建新回复，而是在为当前 story runtime 整理“稍后继续工作用”的累计上下文摘要。
请基于你刚刚继承到的完整上下文提炼真正会影响后续叙事归并和批处理完成的信息。
摘要使用 Markdown 二级标题，按固定顺序组织为：## 当前处理范围、## 已确认叙事、## 新增线索与判断、## 待完成事项。
忽略寒暄、重复内容、无关细节和冗余措辞。
不要直接输出自由文本回复，必须调用 summary 工具；summary 参数应是简洁但信息完整的中文字符串。
</system_reminder>`
}
