package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 是服务运行所需的全部配置，全部来自环境变量。
type Config struct {
	Addr      string // 监听地址，默认 :8080
	DataDir   string // 数据目录，默认 ./data
	Password  string // 单用户登录密码（必填）
	JWTSecret []byte // 签发 cookie 的密钥
	TokenTTL  int    // 登录有效期（天）
	ForceTLS  bool   // 在反代后面时把 cookie 标记为 Secure
}

// Load 读取环境变量并校验。
func Load() (Config, error) {
	c := Config{
		Addr:     env("ADDR", ":8080"),
		DataDir:  env("DATA_DIR", "./data"),
		Password: env("APP_PASSWORD", ""),
		TokenTTL: envInt("TOKEN_TTL_DAYS", 30),
		ForceTLS: envBool("COOKIE_SECURE", false),
	}

	if c.Password == "" {
		return c, fmt.Errorf("必须设置环境变量 APP_PASSWORD（这是你唯一的登录密码）")
	}

	secret := env("JWT_SECRET", "")
	if secret == "" {
		// 没有显式配置时退化为本地生成的随机密钥：进程重启后需要重新登录，
		// 但绝不会把默认密钥带到生产环境。
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return c, err
		}
		secret = base64.RawURLEncoding.EncodeToString(buf)
	}
	c.JWTSecret = []byte(secret)
	return c, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}
