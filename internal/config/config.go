// Package config 负责加载多供应商 LLM 配置（仿 tutorialsmith 模式）。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Provider 描述一个 LLM 供应商。
type Provider struct {
	BaseURL      string `json:"baseURL"`      // OpenAI 兼容接口根地址，如 https://api.deepseek.com
	APIKeyEnv    string `json:"apiKeyEnv"`    // 读取 API Key 的环境变量名
	DefaultModel string `json:"defaultModel"` // 默认模型名
}

// Config 顶层配置。
type Config struct {
	Providers      map[string]Provider `json:"providers"`
	ActiveProvider string              `json:"activeProvider"`
	Temperature    float64             `json:"temperature"`
	MaxIterations  int                 `json:"maxIterations"`
}

// Load 从指定路径加载配置文件；路径为空时按可执行文件目录查找 config.json。
func Load(path string) (*Config, error) {
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]Provider{}
	}
	if cfg.ActiveProvider == "" {
		cfg.ActiveProvider = "mock"
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 8
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	return &cfg, nil
}

// Active 返回当前启用的供应商；不存在则返回 mock 兜底。
func (c *Config) Active() Provider {
	p, ok := c.Providers[c.ActiveProvider]
	if !ok {
		return Provider{DefaultModel: "mock"}
	}
	return p
}

// APIKey 读取供应商对应的环境变量中的密钥；mock 或无 key 时返回空串。
func (p Provider) APIKey() string {
	if p.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(p.APIKeyEnv)
}

// IsMock 判断该供应商是否走本地 mock（无 baseURL 或模型名为 mock）。
func (p Provider) IsMock() bool {
	return p.BaseURL == "" || p.DefaultModel == "mock"
}

// DefaultConfigPath 返回相对可执行文件所在目录的 config.json 路径，
// 便于从任意工作目录启动。
func DefaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(filepath.Dir(exe), "config.json")
}
