package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTruncateJSON(t *testing.T) {
	// 真实场景：多元素数组，元素含较长字段 → 应结构化裁剪且保持合法
	items := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		items = append(items, map[string]any{"id": i, "title": "书名" + strings.Repeat("甲", 200)})
	}
	raw, _ := json.Marshal(items)
	out := truncateJSON(string(raw), 1000)
	if !json.Valid([]byte(out)) {
		t.Fatalf("截断后 JSON 不合法: %s", out[:80])
	}
	if !strings.Contains(out, "_truncated") {
		t.Fatalf("应含 _truncated 提示: %s", out[:80])
	}
	if len(out) >= len(raw) {
		t.Fatalf("裁剪应显著缩短: 原 %d 现 %d", len(raw), len(out))
	}
	// 对象场景
	obj := map[string]any{}
	for i := 0; i < 8; i++ {
		obj[strings.Repeat("k", 3)+itoaSafe(i)] = strings.Repeat("v", 300)
	}
	raw2, _ := json.Marshal(obj)
	out2 := truncateJSON(string(raw2), 800)
	if !json.Valid([]byte(out2)) {
		t.Fatalf("对象截断后 JSON 不合法: %s", out2[:80])
	}
}

func TestTruncateJSONShort(t *testing.T) {
	// 短结果不截断
	raw := `{"a":1}`
	if out := truncateJSON(raw, 2000); out != raw {
		t.Fatalf("短结果不应截断: %s", out)
	}
}
