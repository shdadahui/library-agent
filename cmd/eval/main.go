// 评测执行器：基于 data/eval/cases.json 的评测集，驱动 Agent 并校验工具调用序列。
//
// 用法:
//   go run ./cmd/eval -mock                 # mock 模式（确定性，验证评测集与 harness）
//   go run ./cmd/eval                       # 真实 LLM（config.json 指定供应商）
//   go run ./cmd/eval -only renew-01        # 只跑单个用例
//
// 每个用例使用独立内存数据库（种子数据预置），互不污染、可重复执行。
// 退出码：0 全部通过；1 存在失败用例。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shdadahui/library-agent/internal/agent"
	"github.com/shdadahui/library-agent/internal/config"
	"github.com/shdadahui/library-agent/internal/seed"
	"github.com/shdadahui/library-agent/internal/service"
	"github.com/shdadahui/library-agent/internal/store"
)

// ArgCheck 参数断言：某工具调用的参数值需包含子串。
type ArgCheck struct {
	Tool     string `json:"tool"`
	Param    string `json:"param"`
	Contains string `json:"contains"`
}

// HistoryMsg 多轮上下文（可选）。
type HistoryMsg struct {
	Role    string `json:"role"` // user / assistant
	Content string `json:"content"`
}

// EvalCase 评测用例。
type EvalCase struct {
	ID            string       `json:"id"`
	Category      string       `json:"category"`
	Patron        string       `json:"patron"`
	Input         string       `json:"input"`
	History       []HistoryMsg `json:"history,omitempty"`
	ExpectedTools []string     `json:"expected_tools"`
	ToolOrder     string       `json:"tool_order"` // exact / contains（默认 contains）
	CheckArgs     []ArgCheck   `json:"check_args"`
	RealOnly      bool         `json:"real_only,omitempty"` // 仅真实 LLM 模式（mock 无状态不适用）
	Note          string       `json:"note"`
}

// Suite 评测集文件结构。
type Suite struct {
	Meta  map[string]any `json:"meta"`
	Cases []EvalCase     `json:"cases"`
}

// CaseResult 单个用例结果。
type CaseResult struct {
	ID           string   `json:"id"`
	Category     string   `json:"category"`
	Input        string   `json:"input"`
	Pass         bool     `json:"pass"`
	Reason       string   `json:"reason"`
	ActualTools  []string `json:"actual_tools"`
	FinalText    string   `json:"final_text,omitempty"`
	LatencySec   float64  `json:"latency_sec"`
}

// collector 事件收集器。
type collector struct {
	tools     []string
	argsByName map[string][]string
	text      strings.Builder
	errMsg    string
}

func (c *collector) emit(ev agent.Event) {
	if os.Getenv("EVAL_DEBUG") == "1" {
		b, _ := json.Marshal(ev)
		fmt.Println("EVENT:", string(b))
	}
	switch ev.Type {
	case "tool_call":
		data, _ := ev.Data.(map[string]any)
		name, _ := data["name"].(string)
		args, _ := data["arguments"].(string)
		c.tools = append(c.tools, name)
		c.argsByName[name] = append(c.argsByName[name], args)
	case "message":
		data, _ := ev.Data.(map[string]any)
		if d, ok := data["delta"].(string); ok {
			c.text.WriteString(d)
		}
	case "error":
		data, _ := ev.Data.(map[string]any)
		if m, ok := data["message"].(string); ok {
			c.errMsg = m
		}
	}
}

func main() {
	casesPath := flag.String("cases", "data/eval/cases.json", "评测集路径")
	cfgPath := flag.String("config", "config.json", "LLM 配置路径")
	mock := flag.Bool("mock", false, "强制 mock 模式（不调用真实 LLM）")
	only := flag.String("only", "", "只运行指定用例 ID")
	outPath := flag.String("out", "", "报告输出路径（默认 data/eval/report-<ts>.json）")
	flag.Parse()

	data, err := os.ReadFile(*casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取评测集失败: %v\n", err)
		os.Exit(2)
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		fmt.Fprintf(os.Stderr, "解析评测集失败: %v\n", err)
		os.Exit(2)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil && !*mock {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v（可加 -mock 用本地模式）\n", err)
		os.Exit(2)
	}
	if cfg == nil {
		cfg = &config.Config{
			Providers: map[string]config.Provider{"mock": {DefaultModel: "mock"}},
			ActiveProvider: "mock", Temperature: 0.7, MaxIterations: 8,
		}
	}

	pass, fail, skipped := 0, 0, 0
	var results []CaseResult
	start := time.Now()

	for _, c := range suite.Cases {
		if *only != "" && c.ID != *only {
			continue
		}
		if *mock && c.RealOnly {
			fmt.Printf("[SKIP] %-10s %-8s %s（仅真实 LLM 模式）\n", c.ID, c.Category, c.Input)
			skipped++
			continue
		}
		r := runCase(c, cfg, *mock)
		if r.Pass {
			pass++
		} else {
			fail++
		}
		results = append(results, r)
		status := "PASS"
		if !r.Pass {
			status = "FAIL"
		}
		fmt.Printf("[%s] %-10s %-18s %s\n", status, c.ID, c.Category, c.Input)
		fmt.Printf("      tools=%v 期望=%v 原因: %s\n", r.ActualTools, c.ExpectedTools, r.Reason)
	}

	elapsed := time.Since(start)
	total := pass + fail
	rate := 0.0
	if total > 0 {
		rate = float64(pass) / float64(total) * 100
	}
	fmt.Printf("\n==== 评测汇总 ====\n")
	fmt.Printf("用例 %d | 通过 %d | 失败 %d | 跳过 %d | 通过率 %.1f%% | 耗时 %s | 模式 %s\n",
		total, pass, fail, skipped, rate, elapsed.Round(time.Millisecond), providerMode(cfg, *mock))

	report := map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"mode":         providerMode(cfg, *mock),
		"total":        total, "pass": pass, "fail": fail, "skipped": skipped, "pass_rate": rate,
		"elapsed_sec": elapsed.Seconds(),
		"results":     results,
	}
	out := *outPath
	if out == "" {
		out = fmt.Sprintf("data/eval/report-%s.json", time.Now().Format("20060102-150405"))
	}
	if dir := filepath.Dir(out); dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Printf("报告写入失败: %v\n", err)
	} else {
		fmt.Printf("报告已保存: %s\n", out)
	}

	if fail > 0 {
		os.Exit(1)
	}
}

// runCase 在隔离内存库中执行单个用例。
func runCase(c EvalCase, cfg *config.Config, forceMock bool) CaseResult {
	start := time.Now()
	res := CaseResult{ID: c.ID, Category: c.Category, Input: c.Input}

	st, err := store.Open(":memory:")
	if err != nil {
		res.Pass, res.Reason = false, "打开内存库失败: "+err.Error()
		return res
	}
	defer st.Close()
	if _, err := seed.Seed(st, false, 0); err != nil {
		res.Pass, res.Reason = false, "种子初始化失败: "+err.Error()
		return res
	}

	svc := service.New(st)
	loopCfg := cfg
	if forceMock {
		loopCfg = &config.Config{
			Providers:      map[string]config.Provider{"mock": {DefaultModel: "mock"}},
			ActiveProvider: "mock", Temperature: 0.7, MaxIterations: 8,
		}
	}
	loop := agent.NewLoop(loopCfg, svc)

	patron, err := findPatron(svc, c.Patron)
	if err != nil {
		res.Pass, res.Reason = false, err.Error()
		return res
	}

	col := &collector{argsByName: map[string][]string{}}
	history := make([]agent.Message, 0, len(c.History))
	for _, h := range c.History {
		history = append(history, agent.Message{Role: h.Role, Content: h.Content})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err = loop.Run(ctx, patron, history, c.Input, col.emit)
	res.FinalText = truncate(col.text.String(), 120)
	res.LatencySec = time.Since(start).Seconds()
	if err != nil {
		res.Pass, res.Reason = false, "执行出错: "+err.Error()
		return res
	}

	pass, reason := judge(c, col)
	res.Pass, res.Reason = pass, reason
	res.ActualTools = col.tools
	return res
}

// judge 判定用例：工具序列 + 参数断言。
func judge(c EvalCase, col *collector) (bool, string) {
	exp := c.ExpectedTools
	if len(exp) == 0 {
		if len(col.tools) == 0 {
			return true, "无工具调用，符合预期"
		}
		return false, fmt.Sprintf("期望不调用工具，实际调用了 %v", col.tools)
	}
	ok, why := matchTools(col.tools, exp, c.ToolOrder)
	if !ok {
		return false, why
	}
	for _, ac := range c.CheckArgs {
		argsList := col.argsByName[ac.Tool]
		if len(argsList) == 0 {
			return false, fmt.Sprintf("期望调用 %s 但未调用", ac.Tool)
		}
		if !argContains(argsList[0], ac.Param, ac.Contains) {
			return false, fmt.Sprintf("%s 参数 %s 未包含「%s」（实际: %s）", ac.Tool, ac.Param, ac.Contains, truncate(argsList[0], 80))
		}
	}
	return true, "工具序列与参数断言全部通过"
}

// matchTools 匹配工具序列。
func matchTools(actual, expected []string, order string) (bool, string) {
	if order == "exact" {
		if len(actual) != len(expected) {
			return false, fmt.Sprintf("工具数不符: 期望 %v，实际 %v", expected, actual)
		}
		for i := range expected {
			if actual[i] != expected[i] {
				return false, fmt.Sprintf("第 %d 步工具不符: 期望 %s，实际 %s", i+1, expected[i], actual[i])
			}
		}
		return true, ""
	}
	// contains：expected 为 actual 的子序列（保序）
	i := 0
	for _, a := range actual {
		if i < len(expected) && a == expected[i] {
			i++
		}
	}
	if i == len(expected) {
		return true, ""
	}
	return false, fmt.Sprintf("期望子序列 %v，实际 %v（缺失 %s）", expected, actual, expected[i:])
}

// argContains 解析工具参数 JSON，检查 param 字段值包含子串。
func argContains(argJSON, param, sub string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(argJSON), &m); err != nil {
		return false
	}
	v, ok := m[param]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case string:
		return strings.Contains(t, sub)
	case float64:
		return strings.Contains(fmt.Sprintf("%.0f", t), sub)
	default:
		return false
	}
}

func findPatron(svc *service.Service, name string) (*store.Patron, error) {
	ps, err := svc.Patrons()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.Name == name {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("评测集读者 %s 不存在于种子数据", name)
}

func providerMode(cfg *config.Config, mock bool) string {
	if mock {
		return "mock"
	}
	return cfg.ActiveProvider
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
