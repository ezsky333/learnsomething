// Command crawler 把 xiaolincoding.com 的面试题文档抓取成本地结构化内容。
//
// 用法（一次性）：
//
//	go run ./cmd/crawler -out ./content -raw ./_rawimg
//
// 它会：
//  1. 抓取 16 篇文档的 HTML；
//  2. 解析成「章节 → 题目」两级结构，正文保留 HTML（代码块已带 Prism token）；
//  3. 把 <img> 的 src 改写为本地 /assets/img/<sha1>.<ext>，并把原图下载到 -raw 目录；
//  4. 输出 content/docs/<slug>.json 与 content/manifest.json。
//
// 图片的体积压缩由 scripts/optimize-images.ps1 用 ffmpeg 完成（一次性），
// 转换后覆盖到 content/img/。
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/atom"

	"learnsomething/internal/model"
)

type docSpec struct {
	Slug  string
	Group string
	Order int
	Title string // 兜底标题
}

// 学习顺序按索引页的分篇来排。
var specs = []docSpec{
	{"java", "01 Java 面试篇", 1, "Java 基础面试题"},
	{"collections", "01 Java 面试篇", 2, "Java 集合面试题"},
	{"juc", "01 Java 面试篇", 3, "Java 并发编程面试题"},
	{"jvm", "01 Java 面试篇", 4, "Java 虚拟机面试题"},
	{"spring", "01 Java 面试篇", 5, "Spring 面试题"},
	{"mysql", "05 数据库面试篇", 6, "MySQL 面试题"},
	{"redis", "05 数据库面试篇", 7, "Redis 面试题"},
	{"os", "06 计算机基础面试篇", 8, "操作系统面试题"},
	{"network", "06 计算机基础面试篇", 9, "计算机网络面试题"},
	{"data", "06 计算机基础面试篇", 10, "数据结构与算法面试题"},
	{"mq", "07 架构设计面试篇", 11, "消息队列面试题"},
	{"cap", "07 架构设计面试篇", 12, "分布式面试题"},
	{"systemdesign", "07 架构设计面试篇", 13, "系统设计面试题"},
	{"linux", "08 工具命令面试篇", 14, "Linux 命令面试题"},
	{"git", "08 工具命令面试篇", 15, "Git 面试题"},
	{"docker", "08 工具命令面试篇", 16, "Docker 面试题"},
}

const baseURL = "https://xiaolincoding.com/interview/"

func main() {
	outDir := flag.String("out", "./content", "内容输出目录")
	rawDir := flag.String("raw", "./_rawimg", "图片原始下载目录")
	only := flag.String("only", "", "只抓取指定 slug，逗号分隔")
	flag.Parse()

	if err := os.MkdirAll(filepath.Join(*outDir, "docs"), 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(*outDir, "img"), 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*rawDir, 0o755); err != nil {
		fatal(err)
	}

	client := &http.Client{Timeout: 60 * time.Second}

	manifest := model.Manifest{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	var imgJobs []imgJob
	var imgMu sync.Mutex

	for _, spec := range specs {
		if *only != "" && !strings.Contains(","+*only+",", ","+spec.Slug+",") {
			continue
		}
		doc, jobs, err := crawlDoc(client, spec, *rawDir)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", spec.Slug, err))
		}
		imgMu.Lock()
		imgJobs = append(imgJobs, jobs...)
		imgMu.Unlock()

		data, err := json.Marshal(doc)
		if err != nil {
			fatal(err)
		}
		target := filepath.Join(*outDir, "docs", spec.Slug+".json")
		if err := os.WriteFile(target, data, 0o644); err != nil {
			fatal(err)
		}
		manifest.Docs = append(manifest.Docs, doc.DocMeta)
		fmt.Printf("%-13s sections=%-3d questions=%-4d chars=%-7d json=%dKB\n",
			spec.Slug, doc.SectionCount, doc.QuestionCount, doc.CharCount, len(data)/1024)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*outDir, "manifest.json"), data, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("manifest: %d docs\n", len(manifest.Docs))

	downloadImages(client, imgJobs, *rawDir)
}

type imgJob struct {
	URL  string
	Name string // <sha1>.<ext>
}

func crawlDoc(client *http.Client, spec docSpec, rawDir string) (model.Doc, []imgJob, error) {
	url := baseURL + spec.Slug + ".html"
	resp, err := client.Get(url)
	if err != nil {
		return model.Doc{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return model.Doc{}, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	root, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return model.Doc{}, nil, err
	}

	content := root.Find("div.theme-default-content").First()
	if content.Length() == 0 {
		return model.Doc{}, nil, fmt.Errorf("找不到正文容器")
	}

	// 清掉导航锚点、外链图标、赞赏/公众号尾巴。
	content.Find("a.header-anchor").Remove()
	content.Find("span.sr-only").Remove()
	content.Find("svg.icon.outbound").Remove()

	// 正文里的推广横幅：<p align="center"><a href=".../other/xxx.html"><img ...></a></p>
	// 整段删掉，图片和链接都没有保留价值。
	content.Find("p").Each(func(_ int, s *goquery.Selection) {
		a := s.Find("a").First()
		if a.Length() == 0 || s.Find("img").Length() == 0 {
			return
		}
		if strings.TrimSpace(s.Text()) != "" {
			return
		}
		href, _ := a.Attr("href")
		if strings.Contains(href, "xiaolincoding.com/other/") || strings.Contains(href, "xiaolincoding.com/project/") {
			s.Remove()
		}
	})

	var jobs []imgJob
	content.Find("img").Each(func(_ int, s *goquery.Selection) {
		src, ok := s.Attr("src")
		if !ok || strings.TrimSpace(src) == "" {
			s.Remove()
			return
		}
		if strings.Contains(src, "公众号") || strings.Contains(src, "xiaolincoder/ImageHost3") {
			s.Remove()
			return
		}
		name := imageName(src)
		jobs = append(jobs, imgJob{URL: src, Name: name})
		s.SetAttr("src", "/assets/img/"+name)
		s.SetAttr("loading", "lazy")
		if _, ok := s.Attr("alt"); !ok {
			s.SetAttr("alt", "")
		}
	})
	// 图片外面还可能包一层 <a>（指向推广页），把 <a> 拆掉只留图。
	content.Find("a").Each(func(_ int, s *goquery.Selection) {
		if s.Find("img").Length() == 0 {
			return
		}
		inner, err := s.Html()
		if err != nil {
			return
		}
		s.ReplaceWithHtml(inner)
	})

	promo := regexp.MustCompile(`(?i)公众号|扫码|加群|交流群|最新.*文章.*首发`)

	var (
		sections []model.Section
		curSec   *model.Section
		curItem  *model.Item // 尚未落盘的那道题
		introBuf strings.Builder
		title    = spec.Title
		ids      = map[string]int{}
	)

	// flushItem 把正在收集的题目收进当前章节（空题目丢弃）。
	flushItem := func() {
		if curItem == nil {
			return
		}
		item := curItem
		curItem = nil
		item.HTML = strings.TrimSpace(item.HTML)
		item.Text = strings.TrimSpace(item.Text)
		item.CharCount = len([]rune(item.Text))
		if item.CharCount == 0 && item.HTML == "" {
			return
		}
		if curSec == nil {
			curSec = &model.Section{ID: spec.Slug + "-q", Title: spec.Title}
		}
		curSec.Items = append(curSec.Items, *item)
	}

	// flushSec 收尾当前章节，没有题目的章节丢弃。
	flushSec := func() {
		flushItem()
		if curSec == nil {
			return
		}
		if len(curSec.Items) == 0 {
			curSec = nil
			return
		}
		curSec.Intro = strings.TrimSpace(curSec.Intro)
		sections = append(sections, *curSec)
		curSec = nil
	}

	addBody := func(node *goquery.Selection) {
		htmlStr, err := goquery.OuterHtml(node)
		if err != nil {
			return
		}
		textStr := strings.TrimSpace(node.Text())
		if textStr == "" && !strings.Contains(htmlStr, "<img") && !strings.Contains(htmlStr, "<table") {
			return
		}
		if len([]rune(textStr)) < 120 && promo.MatchString(textStr) {
			return
		}
		if trimmed := strings.TrimSpace(htmlStr); trimmed == "<hr/>" || trimmed == "<hr>" {
			return
		}
		if curItem != nil {
			curItem.HTML += htmlStr + "\n"
			curItem.Text += textStr + "\n"
			return
		}
		if curSec != nil {
			if curSec.Intro != "" {
				curSec.Intro += "\n"
			}
			curSec.Intro += htmlStr
			return
		}
		if introBuf.Len() > 0 {
			introBuf.WriteString("\n")
		}
		introBuf.WriteString(htmlStr)
	}

	hasH3 := content.Find("h3").Length() > 0

	content.Children().Each(func(_ int, node *goquery.Selection) {
		switch node.Get(0).DataAtom {
		case atom.H1:
			if t := cleanTitle(node.Text()); t != "" {
				title = t
			}
			return
		case atom.H2, atom.H3:
			t := cleanTitle(node.Text())
			if t == "" {
				return
			}
			flat := node.Get(0).DataAtom == atom.H2 && !hasH3
			if node.Get(0).DataAtom == atom.H2 && !flat {
				// 二级标题：开一个新章节。
				flushSec()
				curSec = &model.Section{ID: unique(ids, nodeID(node)), Title: t}
				return
			}
			// 题目：扁平页面的 h2，或常规页面的 h3。
			flushItem()
			curItem = &model.Item{ID: unique(ids, nodeID(node)), Title: t}
			return
		}
		addBody(node)
	})
	flushSec()

	// 过滤掉「推荐学习」这类尾部推广章节 / 条目。
	kept := sections[:0]
	for _, s := range sections {
		if strings.Contains(s.Title, "推荐") && len(s.Items) <= 2 {
			continue
		}
		items := s.Items[:0]
		for _, it := range s.Items {
			if skipItemTitle(it.Title) {
				continue
			}
			items = append(items, it)
		}
		s.Items = items
		if len(s.Items) == 0 {
			continue
		}
		kept = append(kept, s)
	}
	sections = kept

	docs := 0
	totalChars := 0
	for i := range sections {
		if sections[i].ID == "" {
			sections[i].ID = fmt.Sprintf("sec-%d", i+1)
		}
		for j := range sections[i].Items {
			if sections[i].Items[j].ID == "" {
				sections[i].Items[j].ID = fmt.Sprintf("%s-%d", sections[i].ID, j+1)
			}
			docs++
			totalChars += sections[i].Items[j].CharCount
		}
	}

	doc := model.Doc{
		DocMeta: model.DocMeta{
			Slug:          spec.Slug,
			Title:         title,
			Group:         spec.Group,
			Order:         spec.Order,
			SourceURL:     url,
			SourceUpdated: sourceUpdated(root),
			SectionCount:  len(sections),
			QuestionCount: docs,
			CharCount:     totalChars,
			Library:       "interview",
		},
		Intro:    strings.TrimSpace(introBuf.String()),
		Sections: sections,
	}
	return doc, jobs, nil
}

func sourceUpdated(root *goquery.Document) string {
	t := strings.TrimSpace(root.Find("footer .last-updated .time").First().Text())
	return t
}

// skipItemTitle 过滤掉纯推广性质的小节（例如「推荐学习」）。
func skipItemTitle(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return true
	}
	for _, bad := range []string{"推荐学习", "关注公众号", "扫我", "赞赏"} {
		if strings.Contains(t, bad) {
			return true
		}
	}
	return false
}

var slugRe = regexp.MustCompile(`[^\p{Han}a-zA-Z0-9]+`)

func cleanTitle(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "#")
	return strings.TrimSpace(s)
}

func nodeID(s *goquery.Selection) string {
	if id, ok := s.Attr("id"); ok && id != "" {
		return sanitizeID(id)
	}
	return sanitizeID(cleanTitle(s.Text()))
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "item"
	}
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}

func unique(counts map[string]int, id string) string {
	counts[id]++
	if n := counts[id]; n > 1 {
		return fmt.Sprintf("%s-%d", id, n)
	}
	return id
}

var extRe = regexp.MustCompile(`(?i)\.(png|jpe?g|webp|gif|svg)(?:#|\?|$)`)

// imageName 生成落盘文件名。
// 统一输出 .webp：scripts/optimize-images.ps1 会把 png/jpg/webp 全部转码成 webp，
// 只有 gif（保留动画）和 svg 原样拷贝，所以扩展名在这里就定死。
func imageName(rawURL string) string {
	sum := sha1.Sum([]byte(rawURL))
	ext := "webp"
	if m := extRe.FindStringSubmatch(rawURL); m != nil {
		switch strings.ToLower(m[1]) {
		case "gif":
			ext = "gif"
		case "svg":
			ext = "svg"
		}
	}
	return hex.EncodeToString(sum[:])[:16] + "." + ext
}

func downloadImages(client *http.Client, jobs []imgJob, rawDir string) {
	seen := map[string]bool{}
	var todo []imgJob
	for _, j := range jobs {
		if seen[j.Name] {
			continue
		}
		seen[j.Name] = true
		if _, err := os.Stat(filepath.Join(rawDir, j.Name)); err == nil {
			continue
		}
		todo = append(todo, j)
	}
	fmt.Printf("images: %d unique, %d to download\n", len(seen), len(todo))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var mu sync.Mutex
	var failed []string
	for _, j := range todo {
		wg.Add(1)
		go func(j imgJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := downloadOne(client, j, rawDir); err != nil {
				mu.Lock()
				failed = append(failed, j.URL+" : "+err.Error())
				mu.Unlock()
			}
		}(j)
	}
	wg.Wait()
	if len(failed) > 0 {
		fmt.Printf("WARN %d images failed:\n", len(failed))
		for _, f := range failed {
			fmt.Println("  ", f)
		}
	}
}

func downloadOne(client *http.Client, j imgJob, rawDir string) error {
	req, err := http.NewRequest(http.MethodGet, j.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/*,*/*;q=0.8")
	// 语雀图床会把 xiaolincoding 的 Referer 当作盗链而返回 403，其余图床正常带上 Referer。
	if strings.Contains(j.URL, "nlark.com") || strings.Contains(j.URL, "yuque.com") {
		req.Header.Set("Referer", "https://www.yuque.com/")
	} else {
		req.Header.Set("Referer", "https://xiaolincoding.com/")
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		tmp := filepath.Join(rawDir, j.Name+".part")
		f, err := os.Create(tmp)
		if err != nil {
			resp.Body.Close()
			return err
		}
		_, err = io.Copy(f, resp.Body)
		f.Close()
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return os.Rename(tmp, filepath.Join(rawDir, j.Name))
	}
	return lastErr
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}

var _ = path.Ext
