package config

import (
	"testing"
)

func TestLoadSensenova(t *testing.T) {
	// 配置包含 sensenova（OpenAI 兼容端点）
	cfg, err := Load("../../config.json")
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	p, ok := cfg.Providers["sensenova"]
	if !ok {
		t.Fatalf("缺少 sensenova 供应商")
	}
	if p.BaseURL != "https://token.sensenova.cn/v1" {
		t.Fatalf("sensenova baseURL 错误: %s", p.BaseURL)
	}
	if p.DefaultModel != "sensenova-6.8-flash-lite" {
		t.Fatalf("sensenova model 错误: %s", p.DefaultModel)
	}
	// APIKey 从环境变量读取
	t.Setenv("SENSENOVA_API_KEY", "sk-test")
	if got := p.APIKey(); got != "sk-test" {
		t.Fatalf("APIKey 读取错误: %s", got)
	}
}

func TestActiveProviderOverride(t *testing.T) {
	cfg, err := Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.ActiveProvider = "sensenova" // 模拟 -provider flag
	if cfg.Active().DefaultModel != "sensenova-6.8-flash-lite" {
		t.Fatalf("运行时切换供应商失败: %s", cfg.Active().DefaultModel)
	}
	// 未知供应商安全降级：回退 mock（不崩溃）
	cfg.ActiveProvider = "nope"
	if cfg.Active().DefaultModel != "mock" {
		t.Fatalf("未知供应商应回退 mock，实际 %s", cfg.Active().DefaultModel)
	}
}
