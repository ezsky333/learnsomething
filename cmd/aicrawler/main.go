// Command aicrawler 抓取「小林面试笔记」的大模型面试题库。
//
// 站点是 VuePress 2 + Theme Hope，结构与前两个站不同：
//   - 正文在 <main id="main-content" class="vp-page">，文章标题在 .vp-page-title > h1
//   - 每篇文章本身就是一道面试题，内部 h2 固定是「💡 简要回答 / 📝 详细解析 / 🎯 面试总结」
//     这些是**讲解层次**，不是可切分的题目，所以一篇 = 一条学习内容
//
// 用法：
//
//	go run ./cmd/aicrawler -out ./content -raw ./_rawimg
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

const baseURL = "https://xiaolinnote.com"

// builtDoc 是抓好的一个专题文档。
type builtDoc struct {
	doc  model.Doc
	jobs []fetcher.Job
}

// topic 是一个专题（侧边栏的一个分组）。
type topic struct {
	Slug  string // 落盘 slug
	Title string // 展示名
	Path  string // 专题导读页
}

// 侧边栏的 6 个分组，顺序即展示顺序。
var topics = []topic{
	{"ai-agent", "Agent 面试题", "/ai/agent/"},
	{"ai-rag", "RAG 面试题", "/ai/rag/"},
	{"ai-tools", "LLM 工具调用面试题", "/ai/tools/"},
	{"ai-llm", "大模型工程面试题", "/ai/llm/"},
	{"ai-langchain", "LangChain 框架面试题", "/ai/langchain/"},
	{"ai-coding", "AI 编程面试题", "/ai/coding/"},
}

func main() {
	outDir := flag.String("out", "./content", "内容输出目录")
	rawDir := flag.String("raw", "./_rawimg", "图片原始下载目录")
	only := flag.String("only", "", "只抓取指定 slug，逗号分隔")
	flag.Parse()

	if err := os.MkdirAll(filepath.Join(*outDir, "docs"), 0o755); err != nil {
		fatal(err)
	}

	fc := fetcher.New(*rawDir)

	var results []builtDoc
	var imgJobs []fetcher.Job
	order := 0

	for _, t := range topics {
		if *only != "" && !strings.Contains(","+*only+",", ","+t.Slug+",") {
			continue
		}
		order++
		doc, jobs, err := crawlTopic(fc, t, order)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", t.Slug, err))
		}
		imgJobs = append(imgJobs, jobs...)
		results = append(results, builtDoc{doc: doc, jobs: jobs})

		data, err := json.Marshal(doc)
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*outDir, "docs", t.Slug+".json"), data, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("%-16s questions=%-3d chars=%-7d json=%dKB\n",
			t.Slug, doc.QuestionCount, doc.CharCount, len(data)/1024)
	}

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

// crawlTopic 抓一个专题的所有题目。
func crawlTopic(fc *fetcher.Client, t topic, order int) (model.Doc, []fetcher.Job, error) {
	indexURL := baseURL + t.Path
	body, err := fc.GetHTML(indexURL)
	if err != nil {
		return model.Doc{}, nil, err
	}
	root, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return model.Doc{}, nil, err
	}

	main := root.Find("main#main-content").First()
	if main.Length() == 0 {
		return model.Doc{}, nil, fmt.Errorf("找不到正文容器")
	}

	// 专题页的「目录」一节就是权威的文章清单与顺序。
	articles := collectArticles(main, t.Path)
	if len(articles) == 0 {
		return model.Doc{}, nil, fmt.Errorf("专题页没有解析出文章列表")
	}

	intro := buildTopicIntro(main)

	var (
		items []model.Item
		jobs  []fetcher.Job
		chars int
	)
	for _, a := range articles {
		item, js, err := crawlArticle(fc, a)
		if err != nil {
			fmt.Printf("  WARN %s: %v\n", a.url, err)
			item = model.Item{ID: a.id, Title: a.title,
				HTML: `<p>（原文暂时抓取失败，请稍后重跑 go run ./cmd/aicrawler 补抓）</p>`}
		}
		items = append(items, item)
		jobs = append(jobs, js...)
		chars += item.CharCount
	}

	doc := model.Doc{
		DocMeta: model.DocMeta{
			Slug:          t.Slug,
			Title:         t.Title,
			Group:         "大模型面试 · 小林面试笔记",
			Order:         order,
			SourceURL:     indexURL,
			SourceUpdated: sourceUpdated(root),
			SectionCount:  1,
			QuestionCount: len(items),
			CharCount:     chars,
			Library:       "ai",
		},
		Intro: intro,
		Sections: []model.Section{{
			ID:    t.Slug + "-main",
			Title: t.Title,
			Items: items,
		}},
	}
	return doc, jobs, nil
}

type article struct {
	id    string
	title string
	url   string
}

// collectArticles 从专题页的「目录」里读出文章清单。
//
// 列表形如 <a href="/ai/agent/1_whatisagent.html">1. 什么是 Agent？…</a>，
// 第一项「Agent 面试题介绍」是专题导读，跳过。
func collectArticles(main *goquery.Selection, topicPath string) []article {
	var out []article
	main.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, ok := a.Attr("href")
		if !ok {
			return
		}
		if !strings.HasSuffix(href, ".html") || !strings.HasPrefix(href, topicPath) {
			return
		}
		title := cleanText(a.Text())
		if title == "" {
			return
		}
		// 「xxx_info.html」是专题导读页，不是题目。
		// 注意不能用「标题含『介绍』」判断 —— 很多题目标题就叫「请你介绍一下…」，
		// 那样会把真题当成导读页漏掉。只认文件名。
		base := strings.TrimSuffix(path.Base(href), ".html")
		if strings.HasSuffix(base, "_info") || base == "index" {
			return
		}
		out = append(out, article{
			id:    base,
			title: title,
			// 目录里的 href 是站内绝对路径，直接拼域名
			url: baseURL + href,
		})
	})
	return dedupe(out)
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

// buildTopicIntro 保留专题页正文（除「目录」以外的说明文字）作为导读。
func buildTopicIntro(main *goquery.Selection) string {
	var sb strings.Builder
	stop := false
	main.Children().Each(func(_ int, n *goquery.Selection) {
		if stop {
			return
		}
		switch n.Get(0).DataAtom {
		case atom.H1:
			return
		case atom.H2:
			if strings.Contains(n.Text(), "目录") {
				stop = true
				return
			}
		case atom.Div:
			// 页面标题 / 元信息区跳过
			if cls, _ := n.Attr("class"); strings.Contains(cls, "vp-page-title") {
				return
			}
		}
		if h, err := goquery.OuterHtml(n); err == nil {
			sb.WriteString(strings.TrimSpace(h))
			sb.WriteString("\n")
		}
	})
	return strings.TrimSpace(sb.String())
}

var promoRe = regexp.MustCompile(`公众号|加群|交流群|扫码|赞赏|打赏|知识星球`)

// crawlArticle 抓取单篇文章正文，一篇 = 一道面试题。
func crawlArticle(fc *fetcher.Client, a article) (model.Item, []fetcher.Job, error) {
	body, err := fc.GetHTML(a.url)
	if err != nil {
		return model.Item{}, nil, err
	}
	root, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return model.Item{}, nil, err
	}
	main := root.Find("main#main-content").First()
	if main.Length() == 0 {
		return model.Item{}, nil, fmt.Errorf("找不到正文容器")
	}

	// 标题在 .vp-page-title > h1；元信息（作者/分类/更新时间）整块丢掉。
	title := a.title
	if h1 := main.Find(".vp-page-title h1").First(); h1.Length() > 0 {
		if t := cleanText(h1.Text()); t != "" {
			title = t
		}
	}
	main.Find(".vp-page-title").Remove()
	main.Find(".vp-breadcrumb").Remove()
	main.Find(".vp-meta-item").Remove()
	main.Find(".update-time").Remove()

	// 锚点、代码块行号、外链图标
	main.Find("a.header-anchor").Remove()
	main.Find(".line-numbers-wrapper").Remove()
	main.Find("span.sr-only").Remove()
	main.Find(".vp-icon").Remove()
	// 正文顶部的空 h1 是标题占位（真实标题已从 .vp-page-title 取走），留着会渲染成空行
	main.Find("h1").Each(func(_ int, h *goquery.Selection) {
		if strings.TrimSpace(h.Text()) == "" {
			h.Remove()
		}
	})

	// 正文末尾的推广尾巴：<hr/> + 「对了，AI Agent的面试题会在「公众号@小林面试笔记」持续更新…」+ 公众号二维码。
	// 整段删掉（含紧跟其后的那张二维码图），否则每篇末尾都挂着一段广告。
	removePromoTail(main)

	// 正文末尾常有「关注公众号」之类的块
	main.Find("div").Each(func(_ int, d *goquery.Selection) {
		if d.Find("div").Length() > 0 {
			return // 只处理叶子块
		}
		t := strings.TrimSpace(d.Text())
		if t != "" && len([]rune(t)) < 60 && promoRe.MatchString(t) {
			d.Remove()
		}
	})

	// 空标题清掉（这类站点常留 <h2 id="🎯-面试总结"></h2> 这种占位）
	main.Find("h1, h2, h3, h4").Each(func(_ int, h *goquery.Selection) {
		if strings.TrimSpace(h.Text()) == "" {
			h.Remove()
		}
	})

	// 抓图 + 改写成本地路径
	var jobs []fetcher.Job
	main.Find("img").Each(func(_ int, img *goquery.Selection) {
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
	})

	// 站内链接改成本站跳转；站外链接统一新窗口
	rewriteLinks(main)

	// 正文真正的容器是 #markdown-content，外面还套着 .vp-content 之类包装层。
	// 直接取 main 的 innerHTML 会带进一堆空 div 与 Vue 注释占位，所以往里收一层。
	inner := main
	if md := main.Find("#markdown-content").First(); md.Length() > 0 {
		inner = md
	} else if vp := main.Find(".vp-content").First(); vp.Length() > 0 {
		inner = vp
	}

	var textSb strings.Builder
	inner.Each(func(_ int, s *goquery.Selection) {
		textSb.WriteString(s.Text())
	})
	text := strings.TrimSpace(textSb.String())
	htmlStr, _ := inner.Html()

	return model.Item{
		ID:        a.id,
		Title:     title,
		HTML:      strings.TrimSpace(htmlStr),
		Text:      text,
		CharCount: len([]rune(text)),
	}, jobs, nil
}

// promoTailRe 匹配文末关注公众号的推广文案。
var promoTailRe = regexp.MustCompile(`持续更新.{0,20}关注|关注起来|别错过最新干货|公众号@|公众号\s*@`)

// removePromoTail 删掉正文末尾的推广段落及其后的二维码图。
//
// 结构形如：
//
//	<hr/><p>对了，…在「<strong>公众号@小林面试笔记题</strong>」持续更新，…别错过最新干货哦！</p>
//	<figure><img src="…二维码….webp"></figure>
//
// 做法：找到最后一个匹配推广文案的块级节点（p / div），
// 把它之后的兄弟节点（含二维码图）全部删掉，再删它自己。
func removePromoTail(scope *goquery.Selection) {
	var hit *goquery.Selection
	scope.Find("p, div, section > div").Each(func(_ int, n *goquery.Selection) {
		t := n.Text()
		if len([]rune(t)) > 300 {
			return // 太长的块是正文，不是推广
		}
		if promoTailRe.MatchString(t) {
			hit = n
		}
	})
	if hit == nil {
		return
	}

	// 先把后面的兄弟节点收齐，再统一删（goquery 的 Remove 会摘掉节点，
	// 一边删一边 Next() 会立刻断链）。
	var doomed []*goquery.Selection
	for n := hit.Next(); n.Length() > 0; n = n.Next() {
		doomed = append(doomed, n)
	}
	for _, n := range doomed {
		n.Remove()
	}
	// 推广段前面紧邻的分隔线也一起收掉
	if prev := hit.Prev(); prev.Length() > 0 && prev.Get(0).DataAtom == atom.Hr {
		prev.Remove()
	}
	hit.Remove()
}
// rewriteLinks 把 /ai/xxx/yyy.html 换成本站应用内跳转，
// 其余站内绝对路径指向原站并新窗口打开。
func rewriteLinks(scope *goquery.Selection) {
	scope.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, ok := a.Attr("href")
		if !ok || href == "" {
			return
		}
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") ||
			strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			// 站内绝对地址（同域名）也按外链处理即可
			if strings.HasPrefix(href, "#") {
				return
			}
			a.SetAttr("target", "_blank")
			a.SetAttr("rel", "noopener noreferrer")
			return
		}
		if strings.HasPrefix(href, "/ai/") && strings.HasSuffix(href, ".html") {
			if slug, itemID, ok := resolveInternalLink(href); ok {
				a.SetAttr("href", fmt.Sprintf("#/doc/%s#item-%s", slug, itemID))
				a.RemoveAttr("target")
				return
			}
		}
		if strings.HasPrefix(href, "/") {
			a.SetAttr("href", baseURL+href)
			a.SetAttr("target", "_blank")
			a.SetAttr("rel", "noopener noreferrer")
		}
	})
}

// resolveInternalLink 把 /ai/<dir>/<file>.html 映射成 (专题 slug, 文章 id)。
func resolveInternalLink(href string) (slug, itemID string, ok bool) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(href, "/ai/"), ".html")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	dir, file := parts[0], parts[1]
	if strings.Contains(file, "/") {
		return "", "", false
	}
	return "ai-" + dir, file, true
}

func sourceUpdated(root *goquery.Document) string {
	t := strings.TrimSpace(root.Find(".update-time").First().Text())
	return strings.TrimSpace(strings.TrimPrefix(t, "更新时间："))
}

func cleanText(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "\u200b", "")
	return strings.TrimSpace(s)
}

// mergeManifest 增量合并：只覆盖本次抓到的 slug，保留其它库的文档。
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
