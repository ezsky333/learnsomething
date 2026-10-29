// Package fetcher 封装抓取文档与图片所需的 HTTP 细节。
package fetcher

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Client 是带重试与并发限制的抓取器。
type Client struct {
	HTTP   *http.Client
	RawDir string // 原图落盘目录
}

// New 创建抓取器。
func New(rawDir string) *Client {
	return &Client{
		HTTP:   &http.Client{Timeout: 60 * time.Second},
		RawDir: rawDir,
	}
}

// 浏览器 UA：部分图床（如语雀 cdn.nlark.com）只对浏览器 UA 返回 200。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// GetHTML 抓取一个页面。
func (c *Client) GetHTML(url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

var extRe = regexp.MustCompile(`(?i)\.(png|jpe?g|webp|gif|svg)(?:#|\?|$)`)

// ImageName 由图片 URL 推导落盘文件名。
//
// 统一输出 .webp：scripts/optimize-images.ps1 会把 png/jpg/webp 全部转码成 webp，
// 只有 gif（保留动画）和 svg 原样拷贝，所以扩展名在这里就定死。
func ImageName(rawURL string) string {
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

// Job 是一张待下载的图片。
type Job struct {
	URL  string
	Name string
}

// DownloadImages 并发下载图片，已存在的跳过。返回失败列表。
func (c *Client) DownloadImages(jobs []Job, concurrency int) (int, []string) {
	if err := os.MkdirAll(c.RawDir, 0o755); err != nil {
		return 0, []string{"创建图片目录失败: " + err.Error()}
	}

	seen := map[string]bool{}
	var todo []Job
	for _, j := range jobs {
		if seen[j.Name] {
			continue
		}
		seen[j.Name] = true
		if _, err := os.Stat(filepath.Join(c.RawDir, j.Name)); err == nil {
			continue
		}
		todo = append(todo, j)
	}
	fmt.Printf("images: %d unique, %d to download\n", len(seen), len(todo))

	if concurrency <= 0 {
		concurrency = 8
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed []string
		sem    = make(chan struct{}, concurrency)
	)
	for _, j := range todo {
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := c.downloadOne(j); err != nil {
				mu.Lock()
				failed = append(failed, j.URL+" : "+err.Error())
				mu.Unlock()
			}
		}(j)
	}
	wg.Wait()
	return len(seen), failed
}

func (c *Client) downloadOne(j Job) error {
	req, err := http.NewRequest(http.MethodGet, j.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/*,*/*;q=0.8")
	// 语雀图床会把 xiaolincoding 的 Referer 当作盗链返回 403，其余图床带上原站 Referer。
	if strings.Contains(j.URL, "nlark.com") || strings.Contains(j.URL, "yuque.com") {
		req.Header.Set("Referer", "https://www.yuque.com/")
	} else {
		req.Header.Set("Referer", "https://xiaolincoding.com/")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.HTTP.Do(req)
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
		tmp := filepath.Join(c.RawDir, j.Name+".part")
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
		return os.Rename(tmp, filepath.Join(c.RawDir, j.Name))
	}
	return lastErr
}
