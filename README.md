# 学习进度 · 面试八股 + 算法教程

一个**自托管**的学习进度 + 笔记系统，内置两个内容库：

- **面试八股**：抓自 [xiaolincoding.com](https://xiaolincoding.com/interview/) 的 16 篇文档，945 道题
- **算法教程**：抓自 [programmercarl.com](https://programmercarl.com/algo/) 的 13 个专题 + 额外题单，274 篇教学长文

内置阅读器、进度标记、笔记、热力图激励，进度和笔记通过 Go 后端 + SQLite 做云端同步，GitHub Actions 打包成 Docker 镜像推到 GitHub Packages。

> 内容版权分别归**小林coding**与**代码随想录**所有，本仓库抓取的内容**仅供个人离线学习**，请勿公开分发。

## 两个内容库的区别

这是本项目最需要注意的设计点，两个站点的内容形态完全不同：

| | 面试八股 | 算法教程 |
| --- | --- | --- |
| 一条学习内容 | 一道面试题 | **一整篇文章** |
| 切分依据 | `h3` 标题即一题 | 文章内部 `h2/h3` 只是讲解层次（思路/总结/各语言题解），**不再切分** |
| 单位 | 题 | 篇 |
| 完成态措辞 | 已掌握 / 未掌握 | 已学完 / 未学完 |
| 篇内导航 | 章节列表 | 上一篇 / 下一篇 |

算法站的文章是**整段教学**：一篇讲透一个知识点（如「二叉树理论基础」），内部的小标题只是行文结构，切开会把一段完整讲解拆成若干碎片。所以算法库采用「一篇文章 = 一条可标记内容」，看完再标记整篇学完。

**跳转一律落在学习模式**：从加星页、笔记页、目录、站内交叉引用跳到某一条内容时，自动切到学习模式（一次只显示一条）并定位到目标，避免在长列表里滚半天找不到。目标在当前版本里已不存在时（内容更新导致旧笔记失效），会明确提示而不是静默显示第一条。

URL 里的条目 id 可能含中文，浏览器会把它百分号编码，所以跳转前必须 `decodeURIComponent` 再查 DOM，否则永远定位不到。

## 有什么

| 能力 | 说明 |
| --- | --- |
| 双内容库 | 面试八股（16 篇 / 945 题）+ 算法教程（14 个专题 / 274 篇），看板一键切换，各自独立进度 |
| 全离线内容 | 1466 张配图全部本地化（132MB），全部内置进二进制，运行时不联外网 |
| 三级进度 | 未开始 / 学习中 / 已掌握（算法库为「已学完」），自动统计百分比 |
| 学习模式 | 一次只显示一条，「完成并下一条」和「下一条」是两个独立按钮 |
| 笔记 | 电脑端着陆在右侧常驻笔记栏，边看边记；每条可写多条，支持轻量 Markdown |
| 加星 | 标重点，独立「加星」页集中回看 |
| 日间 / 夜间 | 明暗两套完整配色，一键切换，也可跟随系统，选择会被记住 |
| 激励 | 总进度环、连续学习天数、今日动作数、26 周热力图、下一篇建议 |
| 阅读体验 | 排版跟随主题、代码高亮、图片点击放大、目录跳转、`j/k` 键盘流 |
| 站内跳转 | 从加星/笔记/正文链接跳到某一条时**自动切到学习模式**并精确落在目标上 |
| 缓存策略 | 内容接口走 ETag 协商，升级镜像后立刻拿到新内容，不会卡在旧版 |
| 云端同步 | 登录后所有标记与笔记存进后端 SQLite，换设备/换浏览器进度都在 |
| 单镜像部署 | 前端构建产物 embed 进 Go 二进制，镜像推到 **GitHub Packages (ghcr.io)** |

## 快速开始

### 用 Docker（推荐）

镜像发布在 **GitHub Packages**（`ghcr.io`），不在 Docker Hub。

```bash
# 如果包是私有的，先登录（用 GitHub 用户名 + PAT，PAT 需 read:packages）
echo <你的PAT> | docker login ghcr.io -u <你的GitHub用户名> --password-stdin

docker run -d --name learn -p 8080:8080 \
  -e APP_PASSWORD='你的登录密码' \
  -e JWT_SECRET='openssl rand -base64 32 生成一串' \
  -v learn-data:/data \
  ghcr.io/OWNER/learnsomething:latest
```

打开 `http://localhost:8080`，输入 `APP_PASSWORD` 即可。

> 镜像默认是私有的。想直接 `docker pull` 不用登录，去仓库 **Packages** 页面把包的可见性改成 public。

### 用 docker compose

```bash
cp docker-compose.yml docker-compose.local.yml
# 改掉 APP_PASSWORD 和 JWT_SECRET
docker compose -f docker-compose.local.yml up -d
```

### 本地开发

需要 Go 1.26+ 与 Node 22+。

```bash
# 1) 前端依赖
cd web && pnpm install

# 2) 后端（另开终端）
$env:APP_PASSWORD='test123'; $env:DATA_DIR='./data'; go run ./cmd/server

# 3) 前端热更新（vite 已把 /api 和 /assets/img 代理到 :8080）
cd web && pnpm dev
```

想直接跑单文件版本（前端 embed 进二进制）：

```bash
cd web && pnpm build && cd ..
go run ./cmd/server      # 访问 http://localhost:8080
```

## 配置

全部通过环境变量：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `APP_PASSWORD` | **必填** | 唯一的登录密码 |
| `JWT_SECRET` | 随机生成 | cookie 签名密钥。不设则每次重启都要重新登录 |
| `ADDR` | `:8080` | 监听地址 |
| `DATA_DIR` | `./data` | SQLite 数据目录，挂卷备份这里即可 |
| `TOKEN_TTL_DAYS` | `30` | 登录有效期 |
| `COOKIE_SECURE` | `false` | 反代在 HTTPS 后面时设为 `true` |

连续 5 次密码错误会锁定该 IP 10 分钟。

## 数据

只有一个文件：`$DATA_DIR/learn.db`（SQLite，WAL 模式）。

```bash
# 备份
docker cp learn:/data/learn.db ./learn-backup.db
```

两张表：`progress`（每题的状态/加星/完成日）和 `notes`（笔记，一题可多条）。

## 重新抓取内容

内容已经提交在 `content/`，平时不需要动。想更新到最新文章时：

```bash
# 1) 面试八股：抓 HTML → 结构化 JSON，下载原图到 _rawimg/
go run ./cmd/crawler -out ./content -raw ./_rawimg

# 2) 算法教程：按专题抓文章，同样落进 content/
go run ./cmd/algocrawler -out ./content -raw ./_rawimg

# 3) 原图压成 webp（需要 ffmpeg 在 PATH 里），800MB → 约 150MB
powershell -ExecutionPolicy Bypass -File scripts/optimize-images.ps1

# 只抓某一部分
go run ./cmd/crawler -out ./content -raw ./_rawimg -only redis,mysql
go run ./cmd/algocrawler -out ./content -raw ./_rawimg -only algo-graph,algo-array
```

两个爬虫各管一个库，`manifest.json` 由它们合并写入（按 slug 覆盖，不会互相清掉）。

**八股爬虫**（`cmd/crawler`）：定位 `div.theme-default-content`，`h2` 当章节、`h3` 当题目（扁平结构的页面把 `h2` 当题目），正文 HTML 原样保留。

**算法爬虫**（`cmd/algocrawler`）：
- 从每个专题页的「学习顺序」列表读出文章的权威清单与顺序
- **一篇文章 = 一条内容**，文章内部的 `h2/h3` 不再切分
- **正文完整保留**，包含「其他语言版本」整节的全部语言题解（Java / C++ / Python / Go / Rust… 一个字都不删）
- 只剔除两类非教学内容：「算法公开课」（站外视频导流）与顶部充值横幅
- 残留的 `/algo/xxx/yyy.html` 站内链接改写为 `#/doc/algo-xxx#item-yyy`，前端路由内跳转
- 「双指针法」是跨专题的复习清单（文章散落在数组/字符串/链表目录），单独放宽目录前缀

抓取完整性可以用「代码块数量对齐」来核对，实测抽样与原站 `<pre>` 数量逐篇一致。

## 镜像构建

GitHub Actions 在 push 到 `main`/`master`、打 `v*` tag、或手动触发时：

1. `go vet` + `go test` + 前端 `vue-tsc` 类型检查和构建
2. 多架构（amd64 + arm64）构建并推送到 **GitHub Packages**：`ghcr.io/<owner>/<repo>`

标签：`latest`（默认分支）、分支名、`sha-xxxxxxx`、`v1.2.3`。用的凭据是仓库自带的 `GITHUB_TOKEN`，**不需要**配置 Docker Hub 账号或任何 secret。

构建产物出现在仓库主页右侧的 **Packages**，点进去就能看到可拉取的标签。

### 可选：推到自己的私有 registry

想改成 Docker Hub 或别的 registry，改 `build-image.yml` 里两处即可：

- `docker/login-action` 的 `registry` / `username` / `password`
- `docker/metadata-action` 的 `images`

本地构建：

```bash
docker build -t learnsomething:local .
# 内容体积大，首次构建会慢一些；后续走 gha/buildkit 缓存
```

## 技术栈

- **后端**：Go 1.26 · 标准库 `net/http`（Go 1.22+ 路由）· `modernc.org/sqlite`（纯 Go，无需 cgo）· `golang-jwt/jwt`
- **前端**：Vue 3.5 · TypeScript · Vite 7 · Pinia · Tailwind CSS v4
- **打包**：多阶段 Dockerfile，前端产物 `go:embed` 进二进制，运行时镜像基于 alpine
- **爬虫**：`goquery`，一次性离线运行

## 目录结构

```
cmd/crawler/        八股题抓取器（小林coding）
cmd/algocrawler/    算法教程抓取器（代码随想录）
cmd/server/         服务器入口
internal/config/    环境变量配置
internal/model/     内容数据结构
internal/fetcher/   HTTP 抓取、图片下载与命名
internal/store/     SQLite 存储层（进度 / 笔记 / meta）
internal/server/    HTTP 路由、认证、静态资源
content/            抓取产物（已提交）：manifest.json + docs/*.json + img/*
web/                Vue 前端
  src/views/        看板（双库切换）/ 阅读（列表+学习模式）/ 笔记 / 登录
  src/components/   进度环、热力图、笔记面板、主题切换等
  src/stores/       内容、学习状态、主题
scripts/            图片批量转 webp
```

## 快捷键（阅读页）

列表模式下：

| 键 | 作用 |
| --- | --- |
| `j` / `k` | 下一题 / 上一题 |
| `1` / `2` / `3` | 学习中 / 已掌握 / 清除 |
| `s` | 加星 / 取消 |
| `n` | 打开当前题笔记 |

学习模式下（一次只显示一条，底部有悬浮操作条）：

| 键 | 作用 |
| --- | --- |
| `Enter` | **已掌握/已学完，下一条**（标记 + 前进） |
| `→` / `←` | 单纯翻页，不改状态 |
| `1` | 学习中 + 前进 |
| `2` / `3` | 已掌握 / 清除（留在当前条） |
| `s` | 加星 |
| `n` | 打开笔记抽屉 |
| `Ctrl/⌘ + Enter` | 保存笔记 |
| `Esc` | 关闭图片放大 / 抽屉 / 退出输入框 |

## 许可

代码 MIT。抓取的文章内容版权归**小林coding**与**代码随想录**所有，仅限个人学习使用。
