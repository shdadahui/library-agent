package agent

import "strings"

// FilterResult 意图预过滤结果。
type FilterResult struct {
	Handled bool   // true 表示本地直接回复，不调用 LLM
	Reply   string // 本地回复文案
}

// 图书馆相关关键词（命中即放行，交由 LLM 处理）。
var libraryKeywords = []string{
	"书", "借", "还", "续", "预约", "排队", "罚款", "欠费", "逾期",
	"到期", "馆藏", "藏书", "读者", "在借", "统计", "多少本", "书架",
	"条码", "查询", "查", "找", "搜", "有没有", "归还", "看什么",
}

// 明确无关主题（强信号词，命中即拦截，不消耗 token）。
// 业务词判定在前，故此处只放语义明确、几乎不会出现在图书馆语境中的词。
var unrelatedKeywords = []string{
	"天气", "股票", "基金", "财经", "比特币", "投资理财", "政治", "新闻",
	"美食", "菜谱", "做饭", "电影", "电视剧", "游戏", "唱歌", "跳舞",
	"化妆", "穿搭", "减肥", "健身", "旅游攻略", "写诗", "一首诗", "作诗",
	"写作文", "写小说", "翻译", "编程", "写代码", "代码", "考试答案",
	"找工作", "租房子", "买房子", "买车", "体育比分", "彩票", "八卦",
	"追星", "情感咨询", "恋爱", "婚姻", "星座", "算命", "笑话",
	"今天几号", "今年是",
}

// 时间/运营类无关词：优先级高于业务词（"图书馆几点关门"含"书"但意图是问时间）。
var timeIrrelevantKeywords = []string{
	"几点开门", "几点关门", "几点开", "几点关", "几点",
	"开放时间", "开馆时间", "营业时间", "工作时间", "上下班",
	"周几开门", "周末开", "节假日开", "闭馆",
}

// 问候/闲聊（命中且无图书馆意图即拦截）。
var chitChatKeywords = []string{
	"你好", "您好", "嗨", "哈喽", "hello", "hi", "在吗", "谢谢", "谢了",
	"辛苦了", "再见", "拜拜", "早安", "晚安", "哈哈", "呵呵", "nihao",
}

var welcomeReply = "您好！我是图书馆智能助手，可以帮您查书、预约、续借、还书，查询罚款和藏书统计。请问需要什么帮助？"
var outOfScopeReply = "抱歉，我只负责图书馆相关的事务（查书、借还、续借、预约、罚款、藏书统计等）。其他问题请咨询相应渠道。"

// PreFilter 意图预过滤：与图书馆无关或纯闲聊时本地回复，避免浪费 LLM token。
// 判定顺序：书名号（核心意图）→ 图书馆业务词（放行，优先于无关词避免误伤）→ 明确无关主题（拦截）→ 问候闲聊（拦截）→ 默认放行（保守）。
func PreFilter(msg string) FilterResult {
	m := strings.ToLower(strings.TrimSpace(msg))
	if m == "" {
		return FilterResult{Handled: true, Reply: welcomeReply}
	}
	// 1. 含书名号 → 图书馆检索意图明确，放行
	if strings.Contains(m, "《") {
		return FilterResult{}
	}
	// 2. 时间/运营类无关 → 拦截（须在业务词前，避免"图书馆"含"书"放行）
	for _, k := range timeIrrelevantKeywords {
		if strings.Contains(m, k) {
			return FilterResult{Handled: true, Reply: outOfScopeReply}
		}
	}
	// 3. 图书馆业务词 → 放行（"图书馆有编程的书"含"书"，先放行避免被"编程"误伤）
	for _, k := range libraryKeywords {
		if strings.Contains(m, k) {
			return FilterResult{}
		}
	}
	// 4. 明确无关主题 → 拦截
	for _, k := range unrelatedKeywords {
		if strings.Contains(m, k) {
			return FilterResult{Handled: true, Reply: outOfScopeReply}
		}
	}
	// 5. 问候/闲聊 → 拦截
	for _, k := range chitChatKeywords {
		if strings.Contains(m, k) {
			return FilterResult{Handled: true, Reply: welcomeReply}
		}
	}
	// 6. 默认放行（避免误伤）
	return FilterResult{}
}

// mock 模式复用同一过滤器。
func (l *Loop) preFilterReply(msg string) (string, bool) {
	fr := PreFilter(msg)
	if fr.Handled {
		return fr.Reply, true
	}
	return "", false
}
