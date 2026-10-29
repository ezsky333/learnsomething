// Command algocrawler 抓取「代码随想录」的算法教程，整理成可逐篇标记进度的学习单元。
//
// 与 cmd/crawler（八股题）的关键区别：
// 算法站的每篇文章本身就是一个完整的学习单元，文章内部的 h2/h3 只是讲解层次
// （如「思路」「总结」「其他语言版本」），**不能**用来切成多道题。
// 因此这里一个专题 = 一个 Section，一篇文章 = 一个 Item。
//
// 用法：
//
//	go run ./cmd/algocrawler -out ./content -raw ./_rawimg
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/atom"

	"learnsomething/internal/fetcher"
	"learnsomething/internal/model"
)

const baseURL = "https://programmercarl.com"

// builtDoc 是抓好的一个专题文档。
type builtDoc struct {
	doc  model.Doc
	jobs []fetcher.Job
}

// topic 是一个专题（左侧导航的一个分组）。
type topic struct {
	Slug  string // 落盘用的 slug
	Title string // 专题名
	Path  string // 专题导读页路径
	Desc  string // 兜底描述
	// CrossTopic 表示该专题是「跨专题的复习清单」，文章散落在其他专题目录下。
	// 双指针就是这种：它的题目来自数组、字符串、链表、哈希表。
	CrossTopic bool
}

// 顺序即推荐学习顺序，来自 /algo/ 首页的「推荐学习顺序」。
var topics = []topic{
	{"algo-fundamentals", "算法基础", "/algo/fundamentals/", "复杂度分析与性能估算", false},
	{"algo-array", "数组", "/algo/array/", "二分、双指针、滑动窗口、前缀和与模拟", false},
	{"algo-linked-list", "链表", "/algo/linked-list/", "指针操作与局部结构变换", false},
	{"algo-hash-table", "哈希表", "/algo/hash-table/", "快速判重、计数与映射", false},
	{"algo-string", "字符串", "/algo/string/", "原地修改、反转与模式匹配", false},
	{"algo-two-pointers", "双指针法", "/algo/two-pointers/", "两个位置协同缩小搜索范围", true},
	{"algo-stack-queue", "栈与队列", "/algo/stack-queue/", "容器互模拟、单调队列与优先队列", false},
	{"algo-binary-tree", "二叉树", "/algo/binary-tree/", "遍历、属性计算、构造与二叉搜索树", false},
	{"algo-backtracking", "回溯算法", "/algo/backtracking/", "组合、切割、子集、排列与棋盘", false},
	{"algo-greedy", "贪心算法", "/algo/greedy/", "从局部最优推导整体最优", false},
	{"algo-dynamic-programming", "动态规划", "/algo/dynamic-programming/", "状态转移、背包、股票与子序列", false},
	{"algo-monotonic-stack", "单调栈", "/algo/monotonic-stack/", "寻找左右第一个更大或更小的元素", false},
	{"algo-graph", "图论", "/algo/graph/", "DFS、BFS、并查集、最小生成树与最短路", false},
}

// 额外题单：专题之外的补充练习，单独成一个 Section。
var extras = []struct {
	Slug, Title, Desc, Path string
}{
	{"algo-other", "额外题目", "进阶题解、算法模板与复杂度补充", "/algo/other/"},
}

// skipArticle 判断文章是否属于「不该混进主线」的内容。
func skipArticle(topicSlug, title, url string) bool {
	// 周末总结是站点的周更导流文，不是知识点。
	if strings.Contains(title, "周末总结") || strings.Contains(url, "weekly-review") {
		return true
	}
	return false
}

func main() {
	outDir := flag.String("out", "./content", "内容输出目录")
	rawDir := flag.String("raw", "./_rawimg", "图片原始下载目录")
	only := flag.String("only", "", "只抓取指定 slug（题库文档名），逗号分隔")
	flag.Parse()

	docsDir := filepath.Join(*outDir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		fatal(err)
	}

	fc := fetcher.New(*rawDir)
	var imgJobs []fetcher.Job
	now := time.Now().UTC().Format(time.RFC3339)


	var results []builtDoc
	order := 0

	allTopics := make([]topic, 0, len(topics)+len(extras))
	allTopics = append(allTopics, topics...)
	for _, e := range extras {
		allTopics = append(allTopics, topic{e.Slug, e.Title, e.Path, e.Desc, false})
	}

	for _, t := range allTopics {
		if *only != "" && !strings.Contains(","+*only+",", ","+t.Slug+",") {
			continue
		}
		order++
		doc, jobs, err := crawlTopic(fc, t, order, now)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", t.Slug, err))
		}
		imgJobs = append(imgJobs, jobs...)
		results = append(results, builtDoc{doc: doc, jobs: jobs})

		data, err := json.Marshal(doc)
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(filepath.Join(docsDir, t.Slug+".json"), data, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("%-24s articles=%-3d chars=%-7d json=%dKB\n",
			t.Slug, doc.QuestionCount, doc.CharCount, len(data)/1024)
	}

	// 合并进 manifest：保留原有 interview 文档，只替换 algo 部分。
	if err := mergeManifest(*outDir, results); err != nil {
		fatal(err)
	}

	n, failed := fc.DownloadImages(imgJobs, 8)
	fmt.Printf("images: %d unique\n", n)
	if len(failed) > 0 {
		fmt.Printf("WARN %d images failed:\n", len(failed))
		for _, f := range failed {
			fmt.Println("  ", f)
		}
	}
}

func crawlTopic(fc *fetcher.Client, t topic, order int, now string) (model.Doc, []fetcher.Job, error) {
	indexURL := baseURL + t.Path
	body, err := fc.GetHTML(indexURL)
	if err != nil {
		return model.Doc{}, nil, err
	}
	root, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return model.Doc{}, nil, err
	}

	content := root.Find("div.theme-default-content").First()
	if content.Length() == 0 {
		return model.Doc{}, nil, fmt.Errorf("找不到专题页正文")
	}

	// 专题页的「学习顺序」列表就是文章的权威清单与顺序。
	articles := collectArticles(content, t.Path, t.CrossTopic)
	if len(articles) == 0 {
		return model.Doc{}, nil, fmt.Errorf("专题页没有解析出文章列表")
	}

	intro := buildTopicIntro(content)

	var (
		items []model.Item
		jobs  []fetcher.Job
		chars int
	)
	for _, a := range articles {
		if skipArticle(t.Slug, a.title, a.url) {
			continue
		}
		item, js, err := crawlArticle(fc, a, t.Slug)
		if err != nil {
			fmt.Printf("  WARN %s: %v\n", a.url, err)
			// 单篇失败不拖垮整个专题，保留条目但标注空正文。
			item = model.Item{ID: a.id, Title: a.title}
		}
		if len(item.HTML) == 0 && item.CharCount == 0 {
			item.HTML = `<p>（原文暂时抓取失败，请稍后重跑 go run ./cmd/algocrawler 补抓）</p>`
		}
		items = append(items, item)
		jobs = append(jobs, js...)
		chars += item.CharCount
	}

	doc := model.Doc{
		DocMeta: model.DocMeta{
			Slug:          t.Slug,
			Title:         t.Title,
			Group:         "算法教程 · 代码随想录",
			Order:         order,
			SourceURL:     indexURL,
			SourceUpdated: sourceUpdated(root),
			SectionCount:  1,
			QuestionCount: len(items),
			CharCount:     chars,
			Library:       "algo",
		},
		Intro: intro,
		Sections: []model.Section{{
			ID:    t.Slug + "-main",
			Title: t.Title,
			Intro: "",
			Items: items,
		}},
	}
	_ = now
	return doc, jobs, nil
}

type article struct {
	id    string
	title string
	url   string
	slug  string // 文件名，用于生成稳定 ID
}

// collectArticles 从专题页的「学习顺序」小节里读出文章清单。
//
// 常规专题：只收「本专题目录下」的 .html，避免把页脚导航混进来。
// 跨专题的复习清单（如双指针）：文章散落在 /algo/array/ 等别的目录，
// 这时放宽到整站 /algo/ 下的 .html。
func collectArticles(content *goquery.Selection, topicPath string, crossTopic bool) []article {
	prefix := topicPath
	if crossTopic {
		prefix = "/algo/"
	}

	// 优先只取「学习顺序」这一节。
	var nodes []*goquery.Selection
	content.Find("h2").EachWithBreak(func(_ int, h *goquery.Selection) bool {
		if !strings.Contains(h.Text(), "学习顺序") {
			return true
		}
		for n := h.Next(); n.Length() > 0; n = n.Next() {
			if n.Get(0).DataAtom == atom.H2 {
				break
			}
			nodes = append(nodes, n)
		}
		return false
	})

	list := extractLinks(nodes, prefix)
	if len(list) > 0 {
		return dedupe(list)
	}

	// 兜底：整个正文里的站内文章链接。
	return dedupe(extractLinks([]*goquery.Selection{content}, prefix))
}

func extractLinks(nodes []*goquery.Selection, prefix string) []article {
	var out []article
	for _, n := range nodes {
		n.Find("a").Each(func(_ int, a *goquery.Selection) {
			href, ok := a.Attr("href")
			if !ok {
				return
			}
			title := cleanText(a.Text())
			if title == "" {
				return
			}
			// 只保留文章页：以 .html 结尾且落在允许的目录前缀下。
			if !strings.HasSuffix(href, ".html") || !strings.HasPrefix(href, prefix) {
				return
			}
			// 排除返回导航、上下篇之类的辅助链接。
			if strings.Contains(title, "返回") || strings.Contains(title, "上一专题") ||
				strings.Contains(title, "下一专题") || strings.Contains(title, "推荐") {
				return
			}
			base := path.Base(strings.TrimSuffix(href, ".html"))
			out = append(out, article{
				id:    base,
				title: title,
				url:   baseURL + href,
				slug:  base,
			})
		})
	}
	return out
}

func dedupe(in []article) []article {
	seen := map[string]bool{}
	out := in[:0]
	for _, a := range in {
		if seen[a.id] {
			continue
		}
		seen[a.id] = true
		out = append(out, a)
	}
	return out
}

// buildTopicIntro 保留专题页的「专题特点 / 核心方法 / 如何学习」，作为导读。
func buildTopicIntro(content *goquery.Selection) string {
	var kept []*goquery.Selection
	stop := false
	content.Children().Each(func(_ int, n *goquery.Selection) {
		if stop {
			return
		}
		switch n.Get(0).DataAtom {
		case atom.H1:
			return
		case atom.H2:
			t := cleanText(n.Text())
			// 「学习顺序」后面是文章清单，「学习建议」是页尾推广，都不进导读。
			// 注意不能像以前那样用 nil 占位 —— 那种写法遇到「学习顺序」在前、
			// 后面紧跟「学习建议」时会误判，这里显式置位。
			if strings.Contains(t, "学习顺序") || strings.Contains(t, "学习建议") {
				stop = true
				return
			}
		}
		kept = append(kept, n)
	})

	var sb strings.Builder
	for _, n := range kept {
		s, err := goquery.OuterHtml(n)
		if err != nil {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" || s == "<hr/>" {
			continue
		}
		sb.WriteString(s)
		sb.WriteString("\n")
	}
	out := sb.String()
	// 导读里也可能带站内文章链接（额外题目页的目录），统一改写。
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div id='r'>" + out + "</div>")); err == nil {
		sel := doc.Find("#r")
		rewriteInternalLinks(sel)
		if h, err := sel.Html(); err == nil {
			return strings.TrimSpace(h)
		}
	}
	return strings.TrimSpace(out)
}

var promoRe = regexp.MustCompile(`知识星球|算法公开课|训练营|扫码|加群|交流群|打赏|kstar|简历|求职|offer|微信号`)

// crawlArticle 抓取单篇文章正文。
func crawlArticle(fc *fetcher.Client, a article, topicSlug string) (model.Item, []fetcher.Job, error) {
	body, err := fc.GetHTML(a.url)
	if err != nil {
		return model.Item{}, nil, err
	}
	root, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return model.Item{}, nil, err
	}
	content := root.Find("div.theme-default-content").First()
	if content.Length() == 0 {
		return model.Item{}, nil, fmt.Errorf("找不到正文容器")
	}

	// 锚点、外链图标、屏幕阅读器文本一律去掉。
	content.Find("a.header-anchor").Remove()
	content.Find("span.sr-only").Remove()
	content.Find("svg.icon.outbound").Remove()

	// 顶部充值/推广横幅（<p align=center><a><img></a></p>）。
	content.Find("p").Each(func(_ int, p *goquery.Selection) {
		if p.Find("img").Length() == 0 || strings.TrimSpace(p.Text()) != "" {
			return
		}
		href, _ := p.Find("a").First().Attr("href")
		if strings.Contains(href, "programmercarl.com/other/") ||
			strings.Contains(href, "programmercarl.com/xunlian/") ||
			strings.Contains(href, "kamacoder.com/i/web/2026") {
			p.Remove()
		}
	})

	// 「算法公开课」是视频导流，整节删掉。
	content.Find("h2").Each(func(_ int, h *goquery.Selection) {
		if !strings.Contains(h.Text(), "算法公开课") {
			return
		}
		removeUntilNextH2(h)
	})

	// 「其他语言版本」整节完整保留，不做任何折叠或截断：
	// 同一题的多语言题解本身就是学习材料，主用语言（Java）的完整题解往往只在这里出现。
	// 结构保持原文 —— h2「其他语言版本」+ 每个语言一个 h3 + 完整代码块。

	// 抓图并改写成本地路径。
	var jobs []fetcher.Job
	content.Find("img").Each(func(_ int, img *goquery.Selection) {
		src, ok := img.Attr("src")
		if !ok || strings.TrimSpace(src) == "" {
			img.Remove()
			return
		}
		if strings.HasPrefix(src, "data:") {
			img.Remove()
			return
		}
		name := fetcher.ImageName(src)
		jobs = append(jobs, fetcher.Job{URL: src, Name: name})
		img.SetAttr("src", "/assets/img/"+name)
		img.SetAttr("loading", "lazy")
		if _, ok := img.Attr("alt"); !ok {
			img.SetAttr("alt", "")
		}
		img.RemoveAttr("style") // 站内用 style 写死宽度，交给 CSS 自适应
	})
	// 图片外层的推广链接去掉，只留图。
	content.Find("a").Each(func(_ int, s *goquery.Selection) {
		if s.Find("img").Length() == 0 {
			return
		}
		if inner, err := s.Html(); err == nil {
			s.ReplaceWithHtml(inner)
		}
	})

	// 去掉推广性质的短段落（长段落里偶尔提到「知识星球」属于正文，不动）。
	content.Find("p").Each(func(_ int, p *goquery.Selection) {
		t := strings.TrimSpace(p.Text())
		if t == "" || len([]rune(t)) > 100 {
			return
		}
		if promoRe.MatchString(t) && p.Find("img").Length() > 0 {
			return // 图留着，删文字会破坏结构
		}
		if promoRe.MatchString(t) {
			p.Remove()
		}
	})

	// 标题：优先用文章页自己的 h1，其次用专题页给的链接文字。
	title := a.title
	if h1 := content.Find("h1").First(); h1.Length() > 0 {
		if t := cleanText(h1.Text()); t != "" {
			title = t
		}
	}
	content.Find("h1").Remove()

	// 站内链接改写成应用内跳转。
	rewriteInternalLinks(content)

	var textSb strings.Builder
	content.Each(func(_ int, s *goquery.Selection) {
		textSb.WriteString(s.Text())
	})

	htmlStr := strings.TrimSpace(childHTML(content))
	return model.Item{
		ID:        a.id,
		Title:     title,
		HTML:      htmlStr,
		Text:      strings.TrimSpace(textSb.String()),
		CharCount: len([]rune(strings.TrimSpace(textSb.String()))),
	}, jobs, nil
}

// removeUntilNextH2 删掉 h 之后、下一个 h2 之前的所有兄弟节点，最后删掉 h 本身。
//
// 关键点：不能一边 Remove 一边用 Next() 前进 —— goquery 的 Remove 会摘掉节点，
// 原节点的 Next() 随即变空，循环会在第一轮就结束。所以先把要删的节点收齐再删。
func removeUntilNextH2(h *goquery.Selection) bool {
	var doomed []*goquery.Selection
	for n := h.Next(); n.Length() > 0; n = n.Next() {
		if n.Get(0).DataAtom == atom.H2 {
			break
		}
		doomed = append(doomed, n)
	}
	for _, n := range doomed {
		n.Remove()
	}
	h.Remove()
	return true
}


// childHTML 取节点的 innerHTML。
func childHTML(s *goquery.Selection) string {
	h, err := s.Html()
	if err != nil {
		return ""
	}
	return h
}

// rewriteInternalLinks 把原站链接改成本站可用的形式。
//
//   - /algo/xxx/yyy.html  → #/doc/algo-xxx#item-yyy（应用内跳转）
//   - 其余站内绝对路径    → 原站完整地址，新窗口打开，点击不会 404
func rewriteInternalLinks(scope *goquery.Selection) {
	scope.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, ok := a.Attr("href")
		if !ok || href == "" {
			return
		}
		// 已经是完整外链或锚点，跳过。
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") ||
			strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			return
		}
		switch {
		case strings.HasPrefix(href, "/algo/") && strings.HasSuffix(href, ".html"):
			if slug, itemID, ok := resolveInternalLink(href); ok {
				a.SetAttr("href", fmt.Sprintf("#/doc/%s#item-%s", slug, itemID))
				a.RemoveAttr("target")
				a.RemoveClass("router-link-active")
				return
			}
			a.SetAttr("href", baseURL+href)
		case strings.HasPrefix(href, "/"):
			a.SetAttr("href", baseURL+href)
		default:
			return
		}
		a.SetAttr("target", "_blank")
		a.SetAttr("rel", "noopener noreferrer")
	})
}

// resolveInternalLink 把原站文章路径映射成本站的 (专题 slug, 文章 id)。
//
// 原站路径形如 /algo/array/0704-binary-search.html：
//   - 目录名 array 对应本站文档 slug algo-array（other 目录是额外题目，单独一篇）；
//   - 文件名去掉 .html 就是文章 id。
func resolveInternalLink(href string) (slug, itemID string, ok bool) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(href, "/algo/"), ".html")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	dir, file := parts[0], parts[1]
	if strings.Contains(file, "/") {
		return "", "", false
	}
	slug = "algo-" + dir
	if dir == "other" {
		slug = "algo-other"
	}
	return slug, file, true
}

func sourceUpdated(root *goquery.Document) string {
	t := strings.TrimSpace(root.Find("footer .last-updated .time").First().Text())
	return t
}

func cleanText(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "#")
	return strings.TrimSpace(s)
}

// mergeManifest 把算法文档并入现有 manifest。
//
// 只替换「本次真的抓到的」那些 slug —— 用 -only 补抓单个专题时，
// 其他算法专题的条目必须原样保留。
func mergeManifest(outDir string, built []builtDoc) error {
	manifestPath := filepath.Join(outDir, "manifest.json")
	var m model.Manifest
	if raw, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("解析已有 manifest: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	rebuilt := make(map[string]bool, len(built))
	for _, b := range built {
		rebuilt[b.doc.Slug] = true
	}

	kept := m.Docs[:0]
	for _, d := range m.Docs {
		if rebuilt[d.Slug] {
			continue
		}
		kept = append(kept, d)
	}
	for _, b := range built {
		kept = append(kept, b.doc.DocMeta)
	}
	m.Docs = kept
	if m.GeneratedAt == "" {
		m.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, data, 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}


