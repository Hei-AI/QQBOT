package napcat

import (
	rootagent "QqBot/internal/agent"
	audiocap "QqBot/internal/capabilities/audio"
	videocap "QqBot/internal/capabilities/video"
	"QqBot/internal/capabilities/vision"
	"QqBot/internal/common"
	"QqBot/internal/config"
	"QqBot/internal/db"
	"QqBot/internal/llm"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// NapcatGateway 负责 NapCat websocket 连接和请求关联。
//
// 它会持久化原始事件、标准化消息事件，并发布 Agent 事件
// 到根事件队列中。
type NapcatGateway struct {
	cfg      *config.Config
	store    *db.Store
	events   *rootagent.EventQueue
	mu       sync.Mutex
	writeMu  sync.Mutex
	conn     *websocket.Conn
	pending  map[string]chan napcatResponse
	images   ImageMessageAnalyzer
	audio    AudioMessageAnalyzer
	videos   VideoMessageAnalyzer
	stopOnce sync.Once
	cancel   context.CancelFunc
}

type napcatResponse struct {
	Status  string `json:"status"`
	Retcode int    `json:"retcode"`
	Data    any    `json:"data"`
	Echo    string `json:"echo"`
	Message string `json:"message"`
	Wording string `json:"wording"`
}

// NewNapcatGateway 创建一个尚未启动的网关。
func NewNapcatGateway(cfg *config.Config, store *db.Store, events *rootagent.EventQueue, llmClient *llm.LLMClient) *NapcatGateway {
	gateway := &NapcatGateway{
		cfg:     cfg,
		store:   store,
		events:  events,
		pending: map[string]chan napcatResponse{},
		images:  NewImageMessageAnalyzer(vision.Agent{Client: llmClient}),
		audio:   NewAudioMessageAnalyzer(audiocap.Agent{Client: llmClient}),
		videos:  NewVideoMessageAnalyzer(videocap.Agent{Client: llmClient}),
	}
	gateway.audio.Log = store.Log
	gateway.videos.Log = store.Log
	return gateway
}

func (g *NapcatGateway) Start(parent context.Context) error {
	if g.cfg.Server.Napcat.WSURL == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	g.cancel = cancel
	go g.connectLoop(ctx)
	return nil
}

func (g *NapcatGateway) Stop() {
	g.stopOnce.Do(func() {
		if g.cancel != nil {
			g.cancel()
		}
		g.mu.Lock()
		if g.conn != nil {
			_ = g.conn.Close()
		}
		g.mu.Unlock()
	})
}

func (g *NapcatGateway) connectLoop(ctx context.Context) {
	delay := time.Duration(g.cfg.Server.Napcat.ReconnectMs) * time.Millisecond
	if delay <= 0 {
		delay = 3 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, g.cfg.Server.Napcat.WSURL, http.Header{})
		if err != nil {
			g.store.Log("warn", "NapCat websocket connect failed", map[string]any{"event": "napcat.gateway.connect_failed", "error": err.Error()})
			time.Sleep(delay)
			continue
		}
		g.mu.Lock()
		g.conn = conn
		g.mu.Unlock()
		g.store.Log("info", "NapCat websocket connected", map[string]any{"event": "napcat.gateway.connected", "wsUrl": g.cfg.Server.Napcat.WSURL})
		g.readLoop(ctx, conn)
		g.mu.Lock()
		if g.conn == conn {
			g.conn = nil
		}
		g.mu.Unlock()
		time.Sleep(delay)
	}
}

func (g *NapcatGateway) readLoop(ctx context.Context, conn *websocket.Conn) {
	events := make(chan map[string]any, 256)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for payload := range events {
				g.handleEventSafely(payload)
			}
		}()
	}
	defer func() {
		close(events)
		workers.Wait()
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			g.store.Log("warn", "NapCat websocket disconnected", map[string]any{"event": "napcat.gateway.disconnected", "error": err.Error()})
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		if echo := common.AsString(payload["echo"]); echo != "" {
			var resp napcatResponse
			_ = json.Unmarshal(data, &resp)
			g.resolve(echo, resp)
			continue
		}
		select {
		case events <- payload:
		case <-ctx.Done():
			return
		}
	}
}

func (g *NapcatGateway) handleEventSafely(payload map[string]any) {
	defer func() {
		if recovered := recover(); recovered != nil {
			g.store.Log("error", "NapCat event handler panicked", map[string]any{
				"event": "napcat.gateway.event_panic",
				"panic": fmt.Sprint(recovered),
			})
		}
	}()
	g.handleEvent(payload)
}

func (g *NapcatGateway) resolve(echo string, resp napcatResponse) {
	g.mu.Lock()
	ch := g.pending[echo]
	delete(g.pending, echo)
	g.mu.Unlock()
	if ch != nil {
		ch <- resp
	}
}

func (g *NapcatGateway) Request(action string, params map[string]any) (any, error) {
	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("NapCat WebSocket 未连接")
	}
	echo := common.NewID()
	ch := make(chan napcatResponse, 1)
	g.mu.Lock()
	g.pending[echo] = ch
	g.mu.Unlock()
	g.writeMu.Lock()
	err := conn.WriteJSON(map[string]any{"action": action, "params": params, "echo": echo})
	g.writeMu.Unlock()
	if err != nil {
		g.resolve(echo, napcatResponse{})
		return nil, err
	}
	timeout := time.Duration(g.cfg.Server.Napcat.RequestTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case resp := <-ch:
		if resp.Status != "ok" || resp.Retcode != 0 {
			if resp.Wording != "" {
				return nil, errors.New(resp.Wording)
			}
			if resp.Message != "" {
				return nil, errors.New(resp.Message)
			}
			return nil, fmt.Errorf("NapCat 返回错误: %d", resp.Retcode)
		}
		return resp.Data, nil
	case <-time.After(timeout):
		g.mu.Lock()
		delete(g.pending, echo)
		g.mu.Unlock()
		return nil, fmt.Errorf("NapCat 请求超时")
	}
}

func (g *NapcatGateway) SendGroupMessage(groupID, message string) (int, error) {
	data, err := g.Request("send_group_msg", map[string]any{"group_id": groupID, "message": parseOutgoingMessage(message)})
	if err != nil {
		return 0, err
	}
	if m, ok := data.(map[string]any); ok {
		if id := db.IntPtr(m["message_id"]); id != nil {
			return *id, nil
		}
	}
	return 0, fmt.Errorf("NapCat 返回结果缺少 message_id")
}

func (g *NapcatGateway) SendPrivateMessage(userID, message string) (int, error) {
	data, err := g.Request("send_private_msg", map[string]any{"user_id": userID, "message": parseOutgoingMessage(message)})
	if err != nil {
		return 0, err
	}
	if m, ok := data.(map[string]any); ok {
		if id := db.IntPtr(m["message_id"]); id != nil {
			return *id, nil
		}
	}
	return 0, fmt.Errorf("NapCat 返回结果缺少 message_id")
}

func (g *NapcatGateway) handleEvent(payload map[string]any) {
	postType := common.AsString(payload["post_type"])
	messageType := db.StringPtr(payload["message_type"])
	subType := db.StringPtr(payload["sub_type"])
	userID := db.StringPtr(payload["user_id"])
	groupID := db.StringPtr(payload["group_id"])
	var eventTime *time.Time
	if t := db.IntPtr(payload["time"]); t != nil {
		tt := time.Unix(int64(*t), 0)
		eventTime = &tt
	}
	g.store.AddNapcatEvent(db.NapcatEventItem{PostType: postType, MessageType: messageType, SubType: subType, UserID: userID, GroupID: groupID, EventTime: eventTime, Payload: payload})
	if postType == "notice" && common.AsString(payload["notice_type"]) == "group_ban" {
		g.handleGroupBanNotice(payload, groupID, userID, subType, eventTime)
		return
	}
	if postType != "message" || messageType == nil {
		return
	}
	if *messageType == "group" && groupID != nil && !contains(g.cfg.Server.Napcat.ListenGroupIDs, *groupID) {
		g.store.Log("info", "NapCat group message ignored by listenGroupIds", map[string]any{"event": "napcat.message.ignored_group", "groupId": *groupID, "listenGroupIds": g.cfg.Server.Napcat.ListenGroupIDs})
		return
	}
	nickname := ""
	if sender, ok := payload["sender"].(map[string]any); ok {
		nickname = common.AsString(sender["nickname"])
	}
	segments, raw := g.normalizeMessageSegments(payload, deref(groupID))
	if raw == "" {
		raw = common.AsString(payload["raw_message"])
	}
	messageID := db.IntPtr(payload["message_id"])
	item := db.NapcatMessageItem{MessageType: *messageType, SubType: valueOr(subType, "normal"), GroupID: groupID, UserID: userID, Nickname: ptrOrNil(nickname), MessageID: messageID, Message: payload["message"], RawMessage: raw, MessageSegments: segments, EventTime: eventTime, Payload: payload}
	seq := g.store.AddNapcatMessage(item)
	g.store.Log("info", "NapCat message accepted", map[string]any{"event": "napcat.message.accepted", "messageType": *messageType, "groupId": deref(groupID), "userId": deref(userID), "messageSeq": seq, "rawMessage": raw})
	if *messageType == "group" && g.isSelfGroupMessage(payload, userID) {
		g.store.Log("info", "NapCat self group message persisted without publishing to Agent", map[string]any{"event": "napcat.message.self_group_ignored", "groupId": deref(groupID), "userId": deref(userID), "messageSeq": seq})
		return
	}
	eventType := "napcat_group_message"
	if *messageType == "private" {
		eventType = "napcat_private_message"
	}
	event := rootagent.AgentEvent{Type: eventType, Data: map[string]any{"groupId": deref(groupID), "userId": deref(userID), "nickname": nickname, "rawMessage": raw, "messageId": valueInt(messageID), "messageSeq": seq}}
	if eventTime != nil {
		event.At = *eventTime
	}
	g.events.Enqueue(event)
}

func (g *NapcatGateway) handleGroupBanNotice(payload map[string]any, groupID, userID, subType *string, eventTime *time.Time) {
	if groupID != nil && !contains(g.cfg.Server.Napcat.ListenGroupIDs, *groupID) {
		g.store.Log("info", "NapCat group ban notice ignored by listenGroupIds", map[string]any{"event": "napcat.notice.group_ban.ignored_group", "groupId": *groupID, "listenGroupIds": g.cfg.Server.Napcat.ListenGroupIDs})
		return
	}
	duration := valueInt(db.IntPtr(payload["duration"]))
	operatorID := common.AsString(payload["operator_id"])
	noticeSubType := valueOr(subType, common.AsString(payload["sub_type"]))
	if noticeSubType == "" {
		if duration > 0 {
			noticeSubType = "ban"
		} else {
			noticeSubType = "lift_ban"
		}
	}
	isSelf := strings.TrimSpace(g.cfg.Server.Bot.QQ) != "" && deref(userID) == strings.TrimSpace(g.cfg.Server.Bot.QQ)
	event := rootagent.AgentEvent{Type: "napcat_group_ban_notice", Data: map[string]any{
		"groupId":    deref(groupID),
		"userId":     deref(userID),
		"operatorId": operatorID,
		"subType":    noticeSubType,
		"duration":   duration,
		"isSelf":     isSelf,
	}}
	if eventTime != nil {
		event.At = *eventTime
	}
	g.store.Log("info", "NapCat group ban notice accepted", map[string]any{"event": "napcat.notice.group_ban.accepted", "groupId": deref(groupID), "userId": deref(userID), "operatorId": operatorID, "subType": noticeSubType, "duration": duration, "isSelf": isSelf})
	g.events.Enqueue(event)
}

func (g *NapcatGateway) isSelfGroupMessage(payload map[string]any, userID *string) bool {
	if userID == nil || *userID == "" {
		return false
	}
	selfID := common.AsString(payload["self_id"])
	if selfID != "" && selfID == *userID {
		return true
	}
	return strings.TrimSpace(g.cfg.Server.Bot.QQ) != "" && g.cfg.Server.Bot.QQ == *userID
}

func parseOutgoingMessage(message string) []map[string]any {
	segments := []map[string]any{}
	for len(message) > 0 {
		start := strings.Index(message, "[CQ:")
		if start < 0 {
			if message != "" {
				segments = append(segments, map[string]any{"type": "text", "data": map[string]any{"text": message}})
			}
			break
		}
		if start > 0 {
			segments = append(segments, map[string]any{"type": "text", "data": map[string]any{"text": message[:start]}})
		}
		end := strings.Index(message[start:], "]")
		if end < 0 {
			segments = append(segments, map[string]any{"type": "text", "data": map[string]any{"text": message[start:]}})
			break
		}
		code := message[start+4 : start+end]
		parts := strings.Split(code, ",")
		kind := parts[0]
		data := map[string]any{}
		for _, part := range parts[1:] {
			key, value, ok := strings.Cut(part, "=")
			if ok {
				data[key] = value
			}
		}
		switch kind {
		case "at":
			segments = append(segments, map[string]any{"type": "at", "data": data})
		case "image":
			segments = append(segments, map[string]any{"type": "image", "data": data})
		default:
			segments = append(segments, map[string]any{"type": "text", "data": map[string]any{"text": "[CQ:" + code + "]"}})
		}
		message = message[start+end+1:]
	}
	if len(segments) == 0 {
		segments = append(segments, map[string]any{"type": "text", "data": map[string]any{"text": ""}})
	}
	return segments
}

func (g *NapcatGateway) normalizeMessageSegments(payload map[string]any, groupID string) ([]db.MessageSegment, string) {
	rawSegments := normalizeRawSegments(payload["message"])
	out := make([]db.MessageSegment, 0, len(rawSegments))
	var text strings.Builder
	for _, segment := range rawSegments {
		kind := common.AsString(segment["type"])
		data, _ := segment["data"].(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		item := db.MessageSegment{Type: kind, Data: data}
		switch kind {
		case "text":
			item.Text = common.AsString(data["text"])
		case "at":
			qq := common.AsString(data["qq"])
			name := common.AsString(data["name"])
			if name == "" && groupID != "" && qq != "" && qq != "all" {
				name = g.lookupGroupMemberName(groupID, qq)
				if name != "" {
					data["name"] = name
				}
			}
			if qq == "all" {
				item.Text = "@全体成员"
			} else if name != "" {
				item.Text = "@" + name
			} else {
				item.Text = "@" + qq
			}
		case "reply":
			replyID := common.AsString(data["id"])
			preview, sender, sentAt := g.lookupReplyPreview(replyID)
			if preview != "" {
				data["preview"] = preview
				data["sender"] = sender
				data["sentAt"] = sentAt
				item.Text = "[引用 " + sender
				if sentAt != "" {
					item.Text += " " + sentAt
				}
				item.Text += ": " + preview + "]"
			} else {
				item.Text = "[引用消息 " + replyID + "]"
			}
		case "forward":
			rendered := g.lookupForwardPreview(data)
			item.Text = rendered
			if rendered != "[合并转发]" {
				data["preview"] = rendered
			}
		case "image":
			file := firstNonEmpty(common.AsString(data["file"]), common.AsString(data["url"]))
			item.Text = "[图片"
			if file != "" {
				item.Text += ": " + file
			}
			item.Text += "]"
			if imageURL := g.resolveImageURL(data); imageURL != "" {
				if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
					data["url"] = imageURL
				} else {
					data["localFile"] = imageURL
				}
			}
			rendered, err := g.images.AnalyzeImageSegmentWithError(context.Background(), data)
			if err != nil {
				g.store.Log("warn", "NapCat image understanding failed", map[string]any{
					"event": "napcat.image.analyze_failed",
					"file":  common.AsString(data["file"]),
					"url":   truncateRunes(common.AsString(data["url"]), 180),
					"error": err.Error(),
				})
			}
			item.Text = rendered
			if description := extractImageDescription(rendered); description != "" {
				data["summary"] = description
			}
		case "record", "audio":
			rendered := g.audio.AnalyzeAudioSegment(context.Background(), data)
			item.Text = rendered
			if description := extractAudioDescription(rendered); description != "" {
				data["summary"] = description
			}
		case "video":
			rendered := g.videos.AnalyzeVideoSegment(context.Background(), data)
			item.Text = rendered
			if description := extractVideoDescription(rendered); description != "" {
				data["summary"] = description
			}
		case "file":
			filename := common.AsString(data["file"])
			item.Text = "[文件"
			if filename != "" {
				item.Text += ": " + filename
			}
			item.Text += "]"
			switch mediaKindFromFilename(filename) {
			case "audio":
				if fileURL := g.resolveFileURL(groupID, data); fileURL != "" {
					data["url"] = fileURL
					rendered := g.audio.AnalyzeAudioSegment(context.Background(), data)
					item.Text = rendered
					if description := extractAudioDescription(rendered); description != "" {
						data["summary"] = description
					}
				}
			case "video":
				if fileURL := g.resolveFileURL(groupID, data); fileURL != "" {
					data["url"] = fileURL
					rendered := g.videos.AnalyzeVideoSegment(context.Background(), data)
					item.Text = rendered
					if description := extractVideoDescription(rendered); description != "" {
						data["summary"] = description
					}
				}
			case "":
				item.Text = g.renderGenericFileSegment(context.Background(), groupID, data)
			}
		case "face":
			item.Text = "[表情 " + common.AsString(data["id"]) + "]"
		default:
			item.Text = "[" + kind + "]"
		}
		text.WriteString(item.Text)
		out = append(out, item)
	}
	return out, strings.TrimSpace(text.String())
}

func (g *NapcatGateway) resolveImageURL(data map[string]any) string {
	file := strings.TrimSpace(common.AsString(data["file"]))
	if file == "" {
		return ""
	}
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") || strings.HasPrefix(file, "file://") {
		return file
	}
	if _, err := os.Stat(file); err == nil {
		return file
	}
	result, err := g.Request("get_image", map[string]any{"file": file})
	if err != nil {
		g.store.Log("warn", "NapCat image URL resolution failed", map[string]any{
			"event": "napcat.image.resolve_failed",
			"file":  file,
			"error": err.Error(),
		})
		return ""
	}
	payload, _ := result.(map[string]any)
	g.store.Log("info", "NapCat get_image result", map[string]any{
		"event":  "napcat.image.resolve_result",
		"file":   file,
		"result": sanitizeNapcatImageResult(payload),
	})
	copyImagePayload(data, payload)
	resolved := preferredNapcatImageRef(payload)
	if resolved == "" {
		g.store.Log("warn", "NapCat image resolution returned no path", map[string]any{
			"event":  "napcat.image.resolve_empty",
			"file":   file,
			"result": payload,
		})
	}
	return resolved
}

func copyImagePayload(data, payload map[string]any) {
	for _, key := range []string{"base64", "imageBase64", "mimeType", "contentType"} {
		if value := common.AsString(payload[key]); strings.TrimSpace(value) != "" {
			data[key] = value
		}
	}
	if localFile := existingNapcatImagePath(payload); localFile != "" {
		data["localFile"] = localFile
	}
	if imageURL := strings.TrimSpace(common.AsString(payload["url"])); strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		data["url"] = imageURL
	}
}

func preferredNapcatImageRef(payload map[string]any) string {
	if localFile := existingNapcatImagePath(payload); localFile != "" {
		return localFile
	}
	if imageURL := strings.TrimSpace(common.AsString(payload["url"])); imageURL != "" {
		return imageURL
	}
	return firstNonEmpty(common.AsString(payload["file"]), common.AsString(payload["path"]))
}

func existingNapcatImagePath(payload map[string]any) string {
	for _, key := range []string{"file", "path"} {
		candidate := strings.TrimSpace(common.AsString(payload[key]))
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func sanitizeNapcatImageResult(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[key] = sanitizeNapcatImageResultField(key, item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeNapcatImageResult(item))
		}
		return out
	default:
		return v
	}
}

func sanitizeNapcatImageResultField(key string, value any) any {
	text := common.AsString(value)
	if strings.Contains(strings.ToLower(key), "base64") && text != "" {
		return map[string]any{"base64BytesApprox": len(text) * 3 / 4}
	}
	if text != "" {
		return truncateRunes(text, 500)
	}
	return sanitizeNapcatImageResult(value)
}

func (g *NapcatGateway) resolveFileURL(groupID string, data map[string]any) string {
	fileID := common.AsString(data["file_id"])
	if fileID == "" {
		return ""
	}
	action := "get_private_file_url"
	params := map[string]any{"file_id": fileID}
	if groupID != "" {
		action = "get_group_file_url"
		params["group_id"] = groupID
		if busID, ok := data["busid"]; ok {
			params["busid"] = busID
		}
	}
	result, err := g.Request(action, params)
	if err != nil {
		g.store.Log("warn", "NapCat media file URL resolution failed", map[string]any{
			"event":    "napcat.media_file.resolve_failed",
			"action":   action,
			"fileId":   fileID,
			"filename": common.AsString(data["file"]),
			"error":    err.Error(),
		})
		return ""
	}
	payload, _ := result.(map[string]any)
	return strings.TrimSpace(common.AsString(payload["url"]))
}

func (g *NapcatGateway) renderGenericFileSegment(ctx context.Context, groupID string, data map[string]any) string {
	filename := firstNonEmpty(common.AsString(data["file"]), common.AsString(data["name"]), common.AsString(data["filename"]))
	base := "[文件"
	if filename != "" {
		base += ": " + filename
	}
	base += "]"
	fileURL := firstNonEmpty(common.AsString(data["url"]), g.resolveFileURL(groupID, data))
	if fileURL == "" {
		return base
	}
	data["url"] = fileURL
	localPath, size, err := g.saveNapcatFile(ctx, fileURL, filename)
	if err != nil {
		g.store.Log("warn", "NapCat file save failed", map[string]any{
			"event":    "napcat.file.save_failed",
			"filename": filename,
			"url":      truncateRunes(fileURL, 180),
			"error":    err.Error(),
		})
		return base + "（下载失败）"
	}
	data["localFile"] = localPath
	data["fileSize"] = size
	text := base + "\n已保存: " + localPath
	if size > 0 {
		text += fmt.Sprintf("\n大小: %d 字节", size)
	}
	if preview, ok := textFilePreview(localPath); ok {
		data["preview"] = preview
		text += "\n[文件内容预览]\n" + preview
	}
	return text
}

func (g *NapcatGateway) saveNapcatFile(ctx context.Context, rawURL, filename string) (string, int64, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", 0, errors.New("empty file url")
	}
	if strings.HasPrefix(rawURL, "file://") {
		path := strings.TrimPrefix(rawURL, "file://")
		path = strings.TrimPrefix(path, "/")
		if _, err := os.Stat(path); err == nil {
			info, _ := os.Stat(path)
			return path, info.Size(), nil
		}
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		if info, err := os.Stat(rawURL); err == nil {
			return rawURL, info.Size(), nil
		}
		return "", 0, fmt.Errorf("unsupported file url: %s", truncateRunes(rawURL, 80))
	}
	timeout := time.Duration(g.cfg.Server.Napcat.RequestTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("download file returned %s", resp.Status)
	}
	const maxBytes int64 = 32 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", 0, err
	}
	if int64(len(body)) > maxBytes {
		return "", 0, fmt.Errorf("file exceeds max download size %d bytes", maxBytes)
	}
	dir := filepath.Join("data", "napcat-files", time.Now().Format("2006-01"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	name := sanitizeNapcatFilename(filename)
	if name == "" {
		name = "file.bin"
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405.000000000")+"-"+name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", 0, err
	}
	return path, int64(len(body)), nil
}

func sanitizeNapcatFilename(filename string) string {
	filename = strings.TrimSpace(filepath.Base(filename))
	if filename == "." || filename == string(filepath.Separator) {
		return ""
	}
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	filename = replacer.Replace(filename)
	return strings.TrimSpace(filename)
}

func textFilePreview(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".json", ".jsonl", ".yaml", ".yml", ".csv", ".tsv", ".log", ".xml", ".html", ".htm", ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".cs", ".c", ".cpp", ".h", ".hpp", ".rs", ".sql":
	default:
		return "", false
	}
	const maxPreviewBytes int64 = 16 << 10
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxPreviewBytes+1))
	if err != nil || len(body) == 0 || strings.ContainsRune(string(body), '\x00') {
		return "", false
	}
	text := strings.ToValidUTF8(string(body), "�")
	if int64(len(body)) > maxPreviewBytes {
		text = truncateRunes(text, int(maxPreviewBytes)) + "\n……文件后续内容已省略"
	}
	return strings.TrimSpace(text), strings.TrimSpace(text) != ""
}

func mediaKindFromFilename(filename string) string {
	filename = strings.ToLower(strings.TrimSpace(filename))
	switch {
	case strings.HasSuffix(filename, ".mp3"),
		strings.HasSuffix(filename, ".wav"),
		strings.HasSuffix(filename, ".aac"),
		strings.HasSuffix(filename, ".ogg"),
		strings.HasSuffix(filename, ".oga"),
		strings.HasSuffix(filename, ".flac"),
		strings.HasSuffix(filename, ".aif"),
		strings.HasSuffix(filename, ".aiff"):
		return "audio"
	case strings.HasSuffix(filename, ".mp4"),
		strings.HasSuffix(filename, ".mpeg"),
		strings.HasSuffix(filename, ".mpe"),
		strings.HasSuffix(filename, ".mov"),
		strings.HasSuffix(filename, ".avi"),
		strings.HasSuffix(filename, ".flv"),
		strings.HasSuffix(filename, ".mpg"),
		strings.HasSuffix(filename, ".webm"),
		strings.HasSuffix(filename, ".wmv"),
		strings.HasSuffix(filename, ".3gp"),
		strings.HasSuffix(filename, ".3gpp"):
		return "video"
	default:
		return ""
	}
}

func normalizeRawSegments(value any) []map[string]any {
	switch x := value.(type) {
	case []any:
		out := []map[string]any{}
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case string:
		return []map[string]any{{"type": "text", "data": map[string]any{"text": x}}}
	default:
		return nil
	}
}

func (g *NapcatGateway) lookupGroupMemberName(groupID, userID string) string {
	data, err := g.Request("get_group_member_info", map[string]any{"group_id": groupID, "user_id": userID, "no_cache": false})
	if err != nil {
		return ""
	}
	m, _ := data.(map[string]any)
	return firstNonEmpty(common.AsString(m["card"]), common.AsString(m["nickname"]))
}

func (g *NapcatGateway) lookupReplyPreview(messageID string) (string, string, string) {
	if messageID == "" {
		return "", "", ""
	}
	data, err := g.Request("get_msg", map[string]any{"message_id": messageID})
	if err != nil {
		return "", "", ""
	}
	m, _ := data.(map[string]any)
	sender := ""
	if sm, ok := m["sender"].(map[string]any); ok {
		sender = firstNonEmpty(common.AsString(sm["card"]), common.AsString(sm["nickname"]), common.AsString(sm["user_id"]))
	}
	if sender == "" {
		sender = firstNonEmpty(common.AsString(m["nickname"]), common.AsString(m["user_id"]), "未知用户")
	}
	raw := renderCompactSegments(m["message"], 0)
	if raw == "" {
		raw = common.AsString(m["raw_message"])
	}
	raw = truncateRunes(strings.TrimSpace(raw), 240)
	sentAt := formatMessageTime(m["time"])
	return raw, sender, sentAt
}

func (g *NapcatGateway) lookupForwardPreview(data map[string]any) string {
	if preview := renderForwardNodes(data["content"], 0); preview != "" {
		return "[合并转发]\n" + truncateRunes(preview, 1800)
	}
	forwardID := firstNonEmpty(common.AsString(data["id"]), common.AsString(data["res_id"]))
	if forwardID == "" {
		return "[合并转发]"
	}
	result, err := g.Request("get_forward_msg", map[string]any{"message_id": forwardID, "id": forwardID})
	if err != nil {
		g.store.Log("warn", "NapCat forward message expansion failed", map[string]any{
			"event":     "napcat.forward.expand_failed",
			"forwardId": forwardID,
			"error":     err.Error(),
		})
		return "[合并转发]"
	}
	payload, _ := result.(map[string]any)
	for _, key := range []string{"messages", "message", "content"} {
		if preview := renderForwardNodes(payload[key], 0); preview != "" {
			return "[合并转发]\n" + truncateRunes(preview, 1800)
		}
	}
	return "[合并转发]"
}

func renderForwardNodes(value any, depth int) string {
	if depth > 2 {
		return "[嵌套转发]"
	}
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	lines := make([]string, 0, min(len(items), 20))
	for i, raw := range items {
		if i >= 20 {
			lines = append(lines, "……其余消息已省略")
			break
		}
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if common.AsString(node["type"]) == "node" {
			if nested, ok := node["data"].(map[string]any); ok {
				node = nested
			}
		}
		sender := "未知用户"
		if senderData, ok := node["sender"].(map[string]any); ok {
			sender = firstNonEmpty(common.AsString(senderData["card"]), common.AsString(senderData["nickname"]), common.AsString(senderData["user_id"]), sender)
		} else {
			sender = firstNonEmpty(common.AsString(node["nickname"]), common.AsString(node["name"]), common.AsString(node["user_id"]), sender)
		}
		content := node["content"]
		if content == nil {
			content = node["message"]
		}
		text := renderCompactSegments(content, depth+1)
		if text == "" {
			text = strings.TrimSpace(common.AsString(node["raw_message"]))
		}
		if text == "" {
			continue
		}
		prefix := sender
		if sentAt := formatMessageTime(node["time"]); sentAt != "" {
			prefix += " " + sentAt
		}
		lines = append(lines, prefix+": "+truncateRunes(text, 360))
	}
	return strings.Join(lines, "\n")
}

func renderCompactSegments(value any, depth int) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var text strings.Builder
		for _, raw := range typed {
			segment, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			kind := common.AsString(segment["type"])
			data, _ := segment["data"].(map[string]any)
			if data == nil {
				data = map[string]any{}
			}
			switch kind {
			case "text":
				text.WriteString(common.AsString(data["text"]))
			case "at":
				text.WriteString("@" + firstNonEmpty(common.AsString(data["name"]), common.AsString(data["qq"])))
			case "face":
				text.WriteString("[表情 " + common.AsString(data["id"]) + "]")
			case "image":
				text.WriteString("[图片]")
			case "record", "audio":
				text.WriteString("[语音]")
			case "video":
				text.WriteString("[视频]")
			case "file":
				text.WriteString("[文件: " + common.AsString(data["file"]) + "]")
			case "reply":
				text.WriteString("[引用消息]")
			case "forward":
				nested := renderForwardNodes(data["content"], depth+1)
				if nested == "" {
					text.WriteString("[嵌套转发]")
				} else {
					text.WriteString("[嵌套转发: " + strings.ReplaceAll(nested, "\n", "；") + "]")
				}
			default:
				if kind != "" {
					text.WriteString("[" + kind + "]")
				}
			}
		}
		return strings.TrimSpace(text.String())
	default:
		return ""
	}
}

func formatMessageTime(value any) string {
	seconds := int64(0)
	switch typed := value.(type) {
	case float64:
		seconds = int64(typed)
	case int:
		seconds = int64(typed)
	case int64:
		seconds = typed
	case string:
		if parsed, err := time.Parse(time.RFC3339, typed); err == nil {
			return parsed.Local().Format("01-02 15:04")
		}
	}
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).Local().Format("01-02 15:04")
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func extractImageDescription(rendered string) string {
	rendered = strings.TrimSpace(rendered)
	if strings.HasPrefix(rendered, "[图片: ") && strings.HasSuffix(rendered, "]") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rendered, "[图片: "), "]"))
	}
	return ""
}

func extractAudioDescription(rendered string) string {
	rendered = strings.TrimSpace(rendered)
	if strings.HasPrefix(rendered, "[语音: ") && strings.HasSuffix(rendered, "]") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rendered, "[语音: "), "]"))
	}
	return ""
}

func extractVideoDescription(rendered string) string {
	rendered = strings.TrimSpace(rendered)
	if strings.HasPrefix(rendered, "[视频: ") && strings.HasSuffix(rendered, "]") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rendered, "[视频: "), "]"))
	}
	return ""
}

func valueOr(ptr *string, fallback string) string {
	if ptr == nil || *ptr == "" {
		return fallback
	}
	return *ptr
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func valueInt(ptr *int) int {
	if ptr == nil {
		return 0
	}
	return *ptr
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
