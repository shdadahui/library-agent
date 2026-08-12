package agent

import (
	"testing"
)

func TestValidateArgsRequired(t *testing.T) {
	def := &ToolDef{
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patron_id": map[string]any{"type": "integer"},
				"slot":      map[string]any{"type": "string", "enum": []any{"morning", "afternoon", "evening"}},
			},
			"required": []any{"patron_id", "slot"},
		},
	}
	if err := validateArgs(def, map[string]any{}); err == nil {
		t.Fatal("缺必填参数应报错")
	}
	if err := validateArgs(def, map[string]any{"patron_id": 1}); err == nil {
		t.Fatal("缺 slot 应报错")
	}
	if err := validateArgs(def, map[string]any{"patron_id": 1, "slot": "afternoon"}); err != nil {
		t.Fatalf("合法参数应通过: %v", err)
	}
}

func TestValidateArgsTypeAndEnum(t *testing.T) {
	def := &ToolDef{
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patron_id": map[string]any{"type": "integer"},
				"slot":      map[string]any{"type": "string", "enum": []any{"morning", "afternoon", "evening"}},
			},
			"required": []any{"patron_id", "slot"},
		},
	}
	// 整数兼容数字字符串（LLM 常见）
	if err := validateArgs(def, map[string]any{"patron_id": "5", "slot": "morning"}); err != nil {
		t.Fatalf("数字字符串应兼容 integer: %v", err)
	}
	// 布尔传给 integer 应报错
	if err := validateArgs(def, map[string]any{"patron_id": true, "slot": "morning"}); err == nil {
		t.Fatal("布尔不应匹配 integer")
	}
	// 枚举外取值应报错
	if err := validateArgs(def, map[string]any{"patron_id": 1, "slot": "noon"}); err == nil {
		t.Fatal("枚举外取值应报错")
	}
}

func TestValidateArgsNoSchema(t *testing.T) {
	if err := validateArgs(&ToolDef{}, map[string]any{"x": 1}); err != nil {
		t.Fatalf("无 schema 不应校验: %v", err)
	}
}
