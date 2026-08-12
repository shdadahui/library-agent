package agent

import "testing"

// TestPreFilterLibrary 图书馆相关问题应放行（交给 LLM）。
func TestPreFilterLibrary(t *testing.T) {
	cases := []string{
		"帮我查一下《三体》",
		"图书馆有乔治·奥威尔的书吗",
		"我要借《三体Ⅱ·黑暗森林》",
		"帮我续借《活着》",
		"我有哪些罚款",
		"图书馆有多少藏书",
		"你们图书馆几点关门", // 保守放行，由 LLM/mock 引导
	}
	for _, c := range cases {
		if fr := PreFilter(c); fr.Handled {
			t.Errorf("图书馆相关问题「%s」不应被拦截", c)
		}
	}
}

// TestPreFilterUnrelated 无关主题应拦截（不消耗 token）。
func TestPreFilterUnrelated(t *testing.T) {
	cases := []string{
		"今天天气怎么样",
		"帮我推荐一支股票",
		"最近有什么政治新闻",
		"帮我写一首诗",
		"帮我写一段 Python 代码",
		"给我讲个笑话",
		"帮我做顿饭的菜谱",
	}
	for _, c := range cases {
		fr := PreFilter(c)
		if !fr.Handled || fr.Reply == "" {
			t.Errorf("无关主题「%s」应被拦截并返回本地回复", c)
		}
	}
}

// TestPreFilterChitChat 纯闲聊应拦截。
func TestPreFilterChitChat(t *testing.T) {
	cases := []string{"你好", "您好", "谢谢", "再见", "在吗", "哈哈"}
	for _, c := range cases {
		if fr := PreFilter(c); !fr.Handled {
			t.Errorf("闲聊「%s」应被拦截", c)
		}
	}
}

// TestPreFilterMixed 含书名号的混合句应放行。
func TestPreFilterMixed(t *testing.T) {
	cases := []string{
		"帮我查一下《三体》，顺便说说今天天气",
		"你好，《活着》有吗",
	}
	for _, c := range cases {
		if fr := PreFilter(c); fr.Handled {
			t.Errorf("含书名号的混合句「%s」不应被拦截", c)
		}
	}
}
