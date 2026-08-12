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

// DBConfig 数据库配置（sqlite / mysql）。
type DBConfig struct {
	Driver string `json:"driver"` // sqlite / mysql
	DSN    string `json:"dsn"`    // sqlite: 文件路径; mysql: user:pass@tcp(host:port)/db?parseTime=true&charset=utf8mb4
}

// RedisConfig Redis 配置（会话存储）。
type RedisConfig struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

// AuthConfig 认证配置。
type AuthConfig struct {
	SessionTTLHours int `json:"session_ttl_hours"` // 会话有效期（小时）
}

// Config 顶层配置。
type Config struct {
	Providers      map[string]Provider `json:"providers"`
	ActiveProvider string              `json:"activeProvider"`
	Temperature    float64             `json:"temperature"`
	MaxIterations  int                 `json:"maxIterations"`
	DB             DBConfig            `json:"db"`
	Redis          RedisConfig         `json:"redis"`
	Auth           AuthConfig          `json:"auth"`
}

// Load 从指定路径加载配置文件；若存在同名目录下的 config.local.json 则字段级覆盖（本机私密配置）。
// 优先级：默认值 < config.json < config.local.json。
func Load(path string) (*Config, error) {
	if path == "" {
		path = "config.json"
	}
	cfg := &Config{
		Providers: map[string]Provider{},
		DB:        DBConfig{Driver: "sqlite", DSN: "data/library.db"},
		Redis:     RedisConfig{Addr: "127.0.0.1:6379"},
		Auth:      AuthConfig{SessionTTLHours: 7 * 24},
	}
	// 加载 config.json
	if err := loadInto(path, cfg); err != nil {
		return nil, fmt.Errorf("加载配置 %s 失败: %w", path, err)
	}
	// 加载 config.local.json（若存在，字段级覆盖）
	local := filepath.Join(filepath.Dir(path), "config.local.json")
	if filepath.Base(path) != "config.local.json" {
		if _, err := os.Stat(local); err == nil {
			if err := loadInto(local, cfg); err != nil {
				return nil, fmt.Errorf("加载本地配置 %s 失败: %w", local, err)
			}
		}
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
	if cfg.DB.Driver == "" {
		cfg.DB.Driver = "sqlite"
	}
	if cfg.DB.DSN == "" {
		cfg.DB.DSN = "data/library.db"
	}
	if cfg.Redis.Addr == "" {
		cfg.Redis.Addr = "127.0.0.1:6379"
	}
	if cfg.Auth.SessionTTLHours <= 0 {
		cfg.Auth.SessionTTLHours = 7 * 24
	}
	return cfg, nil
}

// loadInto 读取 JSON 文件并字段级合并到目标配置。
func loadInto(path string, dst *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	// 先序列化为通用 JSON，再按字段合并
	merged, _ := json.Marshal(raw)
	var partial map[string]json.RawMessage
	if err := json.Unmarshal(merged, &partial); err != nil {
		return err
	}
	cur, _ := json.Marshal(dst)
	var curMap map[string]json.RawMessage
	_ = json.Unmarshal(cur, &curMap)
	for k, v := range partial {
		curMap[k] = v
	}
	final, _ := json.Marshal(curMap)
	return json.Unmarshal(final, dst)
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
