package learnsomething

import "embed"

// ContentFS 是抓取好的学习内容（manifest.json / docs/*.json / img/*）。
//
//go:embed all:content
var ContentFS embed.FS

// WebFS 是前端构建产物。开发时它是空的（只有一个占位文件），
// 真正部署时由 `pnpm build` 覆盖成完整产物。
//
//go:embed all:web/dist
var WebFS embed.FS
