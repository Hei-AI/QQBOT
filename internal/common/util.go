package common

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NewID 返回类似 UUID 的随机标识符。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}

// WriteJSON 按指定状态码写入 JSON 响应。
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// ReadJSON 将请求体解码为 JSON。
func ReadJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	return decoder.Decode(out)
}

// JSONMap 将任意可 JSON 编码的值转换为 map。
func JSONMap(v any) map[string]any {
	data, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// ISO 将时间格式化为 UTC RFC3339Nano。
func ISO(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// ParsePage 读取 page/pageSize 查询参数，并提供默认值。
func ParsePage(r *http.Request) (int, int) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

// QueryString 返回去除空白后的查询参数值。
func QueryString(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}

// ParseTimeQuery 解析 RFC3339 格式的查询参数值。
func ParseTimeQuery(r *http.Request, key string) *time.Time {
	raw := QueryString(r, key)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &t
}

// ContainsFold 检查不区分大小写的子串包含关系。
func ContainsFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// LogLine 向 stdout 写入一行近似结构化日志。
func LogLine(level, message string, metadata map[string]any) {
	data, _ := json.Marshal(metadata)
	log.Printf("[%s] %s %s", strings.ToUpper(level), message, string(data))
}

// JSONNumber 格式化从 JSON 解码出的 float64 值。
func JSONNumber(v float64) string {
	if math.Trunc(v) == v {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// AsString 将常见 JSON 标量值转换为字符串。
func AsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return JSONNumber(x)
	case int:
		return strconv.Itoa(x)
	default:
		return ""
	}
}
