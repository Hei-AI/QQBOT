package agent

import (
	"fmt"
	"strings"
)

func storyMarkdown(title, timestamp, scene string, people []string, raw string) string {
	return fmt.Sprintf(`# %s
- 时间：%s
- 场景：%s
- 人物：%s
- 影响：由消息事件自动沉淀，后续可被长期记忆召回。

起因：群聊中出现了一条新的消息。
经过：
1. %s
结果：该消息已作为一条轻量 story 记录下来。`, title, timestamp, scene, strings.Join(people, "、"), raw)
}
