// Command jdscrawler 抓取「JAVA大神」javadashen.com 的高频面试题库。
//
// 站点是 VuePress 2 + Theme Hope，结构与小林面试笔记同源，但切分粒度不同：
//   - 正文容器 <main id="main-content" class="vp-page">，真实内容在 #markdown-content
//   - 一个页面 = 一个主题（Java基础 / JVM / MySQL …），**h2 才是题目**
//   - h3/h4 是题目内部的讲解层次，不能当成可切分的单元
//
// 但对学习任务来说，「一个主题一个模块」太细了：30 个页面就是 30 个模块，
// 勾选框会变成一堵墙。所以这里把来源页面按主题家族合并成 9 个模块
// （Java 基础 / Java 并发 / JVM / Spring 全家桶 / 数据库 / 消息队列 /
// 分布式与微服务 / 计算机基础 / 综合与软技能），
// 每个模块内部的 section 仍然是原始页面，题目顺序与原文一致。
//
// 用法：
//
//	go run ./cmd/jdscrawler -out ./content -raw ./_rawimg
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/atom"

	"learnsomething/internal/fetcher"
	"learnsomething/internal/model"
)

const (
	baseURL    = "https://javadashen.com"
	pathPrefix = "/%E9%AB%98%E9%A2%91%E9%9D%A2%E8%AF%95%E9%A2%98/"
	// groupName 是所有 jds 文档共用的分组名：看板上它会变成一张卡片墙，
	// 和「算法教程 · 代码随想录」一样，而不是 9 个各含一张卡的标题。
	groupName = "高频面试题 · Java大神"
	// library 是新八股库的标识。旧的 interview 库保留下来只做归档。
	library = "jds"
	// oldLibrary 是即将被归档的旧八股库。
	oldLibrary = "interview"
)

// page 是来源站的一个主题页。
type page struct {
	Name  string // 文件名（URL 编码前的标题）
	Slug  string // 在 doc 内部的 section id 前缀
	Title string // 展示名
}

// module 是一个学习模块（= 一篇 doc）。
type module struct {
	Slug  string
	Title string
	Order int
	Pages []page
}

// 模块划分按「一个大厂后端面试会连着问到的家族」来合并，
// 顺序就是推荐的复习顺序：语言 → 并发 → 虚拟机 → 框架 → 存储 → 中间件 → 分布式 → 基础 → 综合。
var modules = []module{
	{"jds-java-basic", "Java 基础", 1, []page{
		{"Java基础面试题", "java-basic", "Java 基础面试题"},
		{"Java集合框架面试题", "collections", "Java 集合框架面试题"},
		{"Java异常与泛型面试题", "exception", "Java 异常与泛型面试题"},
	}},
	{"jds-concurrency", "Java 并发", 2, []page{
		{"Java并发编程面试题", "concurrency", "Java 并发编程面试题"},
		{"线程池面试题", "thread-pool", "线程池面试题"},
		{"synchronized面试题", "synchronized", "synchronized 面试题"},
	}},
	{"jds-jvm", "JVM", 3, []page{
		{"JVM面试题", "jvm", "JVM 面试题"},
		{"JVM垃圾回收面试题", "jvm-gc", "JVM 垃圾回收面试题"},
	}},
	{"jds-spring", "Spring 全家桶", 4, []page{
		{"Spring框架面试题", "spring", "Spring 框架面试题"},
		{"SpringBoot面试题", "springboot", "Spring Boot 面试题"},
		{"SpringMVC面试题", "springmvc", "Spring MVC 面试题"},
		{"SpringCloud面试题", "springcloud", "Spring Cloud 面试题"},
		{"MyBatis面试题", "mybatis", "MyBatis 面试题"},
	}},
	{"jds-database", "数据库", 5, []page{
		{"MySQL面试题", "mysql", "MySQL 面试题"},
		{"Redis面试题", "redis", "Redis 面试题"},
	}},
	{"jds-mq", "消息队列", 6, []page{
		{"RocketMQ面试题", "rocketmq", "RocketMQ 面试题"},
		{"Kafka面试题", "kafka", "Kafka 面试题"},
		{"RabbitMQ面试题", "rabbitmq", "RabbitMQ 面试题"},
	}},
	{"jds-distributed", "分布式与微服务", 7, []page{
		{"分布式系统面试题", "distributed", "分布式系统面试题"},
		{"Dubbo面试题", "dubbo", "Dubbo 面试题"},
		{"ZooKeeper面试题", "zookeeper", "ZooKeeper 面试题"},
		{"Elasticsearch面试题", "elasticsearch", "Elasticsearch 面试题"},
	}},
	{"jds-cs-basic", "计算机基础", 8, []page{
		{"计算机网络面试题", "network", "计算机网络面试题"},
		{"操作系统面试题", "os", "操作系统面试题"},
		{"算法与数据结构面试题", "datastructure", "算法与数据结构面试题"},
	}},
	{"jds-soft", "系统设计与软技能", 9, []page{
		{"系统设计面试题", "systemdesign", "系统设计面试题"},
		{"设计模式面试题", "pattern", "设计模式面试题"},
		{"项目与简历面试题", "project", "项目与简历面试题"},
		{"HR面试高频题", "hr", "HR 面试高频题"},
		{"新趋势面试题", "trend", "新趋势面试题"},
	}},
}

func main() {
	outDir := flag.String("out", "./content", "内容输出目录")
	rawDir := flag.String("raw", "./_rawimg", "图片原始下载目录")
	only := flag.String("only", "", "只抓取指定模块 slug，逗号分隔")
	flag.Parse()

	if err := os.MkdirAll(filepath.Join(*outDir, "docs"), 0o755); err != nil {
		fatal(err)
	}

	fc := fetcher.New(*rawDir)
	// 图片必须带本站 Referer，否则图床返回 403。
	fc.Referer = baseURL + "/"

	var built []model.Doc
	var imgJobs []fetcher.Job
	totalItems, totalChars := 0, 0

	for _, m := range modules {
		if *only != "" && !strings.Contains(","+*only+",", ","+m.Slug+",") {
			continue
		}
		doc, jobs, err := crawlModule(fc, m)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", m.Slug, err))
		}
		imgJobs = append(imgJobs, jobs...)

		data, err := json.Marshal(doc)
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*outDir, "docs", m.Slug+".json"), data, 0o644); err != nil {
			fatal(err)
		}
		built = append(built, doc)
		totalItems += doc.QuestionCount
		totalChars += doc.CharCount
		fmt.Printf("%-18s sections=%-2d questions=%-4d chars=%-7d json=%dKB\n",
			m.Slug, doc.SectionCount, doc.QuestionCount, doc.CharCount, len(data)/1024)
	}

	if err := mergeManifest(*outDir, built); err != nil {
		fatal(err)
	}
	fmt.Printf("total: %d modules, %d questions, %d chars\n", len(built), totalItems, totalChars)

	n, failed := fc.DownloadImages(imgJobs, 8)
	fmt.Printf("images: %d unique\n", n)
	if len(failed) > 0 {
		fmt.Printf("WARN %d images failed:\n", len(failed))
		for _, f := range failed {
			fmt.Println("  ", f)
		}
	}
}

// crawlModule 抓一个模块下所有主题页，拼成一篇 doc。
func crawlModule(fc *fetcher.Client, m module) (model.Doc, []fetcher.Job, error) {
	var (
		sections []model.Section
		jobs     []fetcher.Job
		questions, chars int
	)

	for _, p := range m.Pages {
		sec, js, err := crawlPage(fc, p)
		if err != nil {
			// 单页失败不整体放弃：题库几百条，一页挂掉就全废代价太大。
			fmt.Printf("  WARN %s: %v\n", p.Name, err)
			continue
		}
		jobs = append(jobs, js...)
		if len(sec.Items) > 0 {
			sections = append(sections, sec)
			questions += len(sec.Items)
			for _, it := range sec.Items {
				chars += it.CharCount
			}
		}
	}

	if len(sections) == 0 {
		return model.Doc{}, nil, fmt.Errorf("没有任何页面抓取成功")
	}

	doc := model.Doc{
		DocMeta: model.DocMeta{
			Slug:          m.Slug,
			Title:         m.Title,
			Group:         groupName,
			Order:         m.Order,
			SourceURL:     baseURL + pathPrefix,
			SectionCount:  len(sections),
			QuestionCount: questions,
			CharCount:     chars,
			Library:       library,
		},
		Sections: sections,
	}
	return doc, jobs, nil
}

// crawlPage 抓一个主题页，把每个 h2 切成一道题。
func crawlPage(fc *fetcher.Client, p page) (model.Section, []fetcher.Job, error) {
	pageURL := baseURL + pathPrefix + url.PathEscape(p.Name) + ".html"
	body, err := fc.GetHTML(pageURL)
	if err != nil {
		return model.Section{}, nil, err
	}
	root, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return model.Section{}, nil, err
	}

	main := root.Find("main#main-content").First()
	if main.Length() == 0 {
		return model.Section{}, nil, fmt.Errorf("找不到正文容器")
	}

	// 先取标题（.vp-page-title 稍后整块删掉），再剥掉导航、页脚、锚点等噪音。
	title := p.Title
	if h1 := main.Find(".vp-page-title h1").First(); h1.Length() > 0 {
		if t := cleanText(h1.Text()); t != "" {
			title = t
		}
	}
	main.Find(".vp-page-title").Remove()
	main.Find(".vp-breadcrumb").Remove()
	main.Find("footer").Remove()
	main.Find("nav").Remove()
	// 锚点是包在标题文字外面的 <a>（<h2><a class="header-anchor"><span>题</span></a></h2>）。
	// 直接 Remove 会把标题文字一起删掉，导致每道题的标题都是空的 ——
	// 这里要拆壳留字，只去掉链接本身。
	main.Find("a.header-anchor").Each(func(_ int, a *goquery.Selection) {
		inner, err := a.Html()
		if err != nil {
			return
		}
		a.ReplaceWithHtml(inner)
	})
	main.Find("span.sr-only").Remove()
	main.Find("iconify-icon").Remove()
	// 行号是装饰：站点用 div.line-numbers 承载，本站样式没覆盖它，
	// 留着只会让代码块左边多出一段空白缩进。
	main.Find("div.line-numbers").Remove()
	main.Find("div.line-numbers-mode").RemoveClass("line-numbers-mode")

	md := main.Find("#markdown-content").First()
	if md.Length() == 0 {
		return model.Section{}, nil, fmt.Errorf("#markdown-content 不存在")
	}

	var (
		jobs    []fetcher.Job
		items   []model.Item
		cur     *model.Item
		secText strings.Builder
		seen    = map[string]bool{}
		n       int
	)

	flush := func() {
		if cur == nil {
			return
		}
		it := *cur
		cur = nil
		it.HTML = strings.TrimSpace(it.HTML)
		it.Text = strings.TrimSpace(it.Text)
		it.CharCount = len([]rune(it.Text))
		if it.CharCount == 0 && it.HTML == "" {
			return
		}
		if skipTitle(it.Title) {
			return
		}
		// 同一页里偶尔会把同一道题写两遍（项目/HR 页尤其明显），去重。
		key := strings.ToLower(strings.TrimSpace(it.Title))
		if seen[key] {
			return
		}
		seen[key] = true
		items = append(items, it)
	}

	md.Children().Each(func(_ int, node *goquery.Selection) {
		switch node.Get(0).DataAtom {
		case atom.H1:
			return // 页面标题已单独取过
		case atom.H2:
			t := cleanText(node.Text())
			if t == "" {
				return
			}
			flush()
			n++
			cur = &model.Item{
				ID:    fmt.Sprintf("%s-%02d", p.Slug, n),
				Title: t,
			}
			return
		}

		// 图片：改成本地路径并登记下载任务。
		node.Find("img").Each(func(_ int, img *goquery.Selection) {
			src, ok := img.Attr("src")
			if !ok || strings.TrimSpace(src) == "" {
				img.Remove()
				return
			}
			// 站内是 /assets/xxx.png 形式的相对路径，先补成绝对地址：
			// 哈希用的是绝对 URL，换站点/换协议时不会和旧图撞名。
			abs := src
			if strings.HasPrefix(src, "/") {
				abs = baseURL + src
			}
			name := fetcher.ImageName(abs)
			jobs = append(jobs, fetcher.Job{URL: abs, Name: name})
			img.SetAttr("src", "/assets/img/"+name)
			img.SetAttr("loading", "lazy")
			if _, ok := img.Attr("alt"); !ok {
				img.SetAttr("alt", "")
			}
		})
		rewriteLinks(node)

		htmlStr, err := goquery.OuterHtml(node)
		if err != nil {
			return
		}
		textStr := strings.TrimSpace(node.Text())
		if textStr == "" && !strings.Contains(htmlStr, "<img") && !strings.Contains(htmlStr, "<table") {
			return
		}
		if cur != nil {
			cur.HTML += htmlStr + "\n"
			cur.Text += textStr + "\n"
			return
		}
		secText.WriteString(htmlStr)
		secText.WriteString("\n")
	})
	flush()

	if len(items) == 0 {
		return model.Section{}, jobs, fmt.Errorf("没有解析出题目")
	}

	return model.Section{
		ID:    "jds-" + p.Slug,
		Title: title,
		Intro: strings.TrimSpace(secText.String()),
		Items: items,
	}, jobs, nil
}

var promoRe = regexp.MustCompile(`公众号|加群|交流群|扫码|赞赏|打赏|知识星球|推荐学习`)

// skipTitle 过滤纯推广条目。
func skipTitle(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return true
	}
	return promoRe.MatchString(t)
}

// rewriteLinks 把站内相对链接补成绝对地址，站外链接统一新窗口打开。
func rewriteLinks(node *goquery.Selection) {
	node.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, ok := a.Attr("href")
		if !ok || href == "" {
			return
		}
		if strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			return
		}
		if strings.HasPrefix(href, "/") {
			href = baseURL + href
			a.SetAttr("href", href)
		}
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
			a.SetAttr("target", "_blank")
			a.SetAttr("rel", "noopener noreferrer")
		}
	})
}

func cleanText(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "\u200b", "")
	return strings.TrimSpace(s)
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// mergeManifest 增量合并本次抓到的模块，并把旧八股库整体标记为归档。
//
// 「归档」的语义是：内容仍然可读、进度与笔记都保留，但不再出现在
// 主学习区，也不再被新任务默认勾选 —— 换题库时旧题库不该凭空消失，
// 也不该继续占着主入口。
func mergeManifest(outDir string, built []model.Doc) error {
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
	for _, d := range built {
		rebuilt[d.Slug] = true
	}

	kept := make([]model.DocMeta, 0, len(m.Docs)+len(built))
	for _, d := range m.Docs {
		if rebuilt[d.Slug] {
			continue
		}
		if d.Library == oldLibrary {
			d.Archived = true
		}
		kept = append(kept, d)
	}
	for _, d := range built {
		kept = append(kept, d.DocMeta)
	}
	m.Docs = kept
	m.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

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
