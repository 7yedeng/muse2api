package gateway

import (
	"encoding/base64"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIKey          string
	SessionKey      []byte
	DataDir         string
	Listen          string
	AdminListen     string
	ProxyURL        string
	GenerateTimeout time.Duration
}

func ConfigFromEnv() (Config, error) {
	c := Config{APIKey: os.Getenv("MUSE2API_API_KEY"), DataDir: os.Getenv("MUSE2API_DATA_DIR"), Listen: os.Getenv("MUSE2API_LISTEN"), AdminListen: os.Getenv("MUSE2API_ADMIN_LISTEN"), ProxyURL: os.Getenv("MUSE2API_PROXY_URL"), GenerateTimeout: 30 * time.Minute}
	var err error
	c.SessionKey, err = base64.StdEncoding.DecodeString(os.Getenv("MUSE2API_SESSION_KEY"))
	if err != nil {
		return c, errors.New("MUSE2API_SESSION_KEY must be base64-encoded 32 bytes")
	}
	if raw := os.Getenv("MUSE2API_GENERATE_TIMEOUT"); raw != "" {
		c.GenerateTimeout, err = time.ParseDuration(raw)
		if err != nil {
			return c, errors.New("MUSE2API_GENERATE_TIMEOUT must be a duration such as 30m")
		}
	}
	return c.Normalized()
}

func (c Config) Normalized() (Config, error) {
	if c.DataDir == "" {
		c.DataDir = "runtime"
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:18688"
	}
	if c.AdminListen == "" {
		c.AdminListen = "127.0.0.1:18689"
	}
	if c.GenerateTimeout == 0 {
		c.GenerateTimeout = 30 * time.Minute
	}
	if len(c.APIKey) < 32 || len(c.APIKey) > 256 || strings.ContainsAny(c.APIKey, " \r\n\t\x00") {
		return c, errors.New("MUSE2API_API_KEY must contain at least 32 characters")
	}
	if len(c.SessionKey) != 32 {
		return c, errors.New("MUSE2API_SESSION_KEY must decode to 32 bytes")
	}
	if c.GenerateTimeout < time.Minute || c.GenerateTimeout > 2*time.Hour {
		return c, errors.New("generation timeout must be between 1m and 2h")
	}
	for _, addr := range []string{c.Listen, c.AdminListen} {
		h, p, e := net.SplitHostPort(addr)
		n, e2 := strconv.Atoi(p)
		if e != nil || e2 != nil || n < 1 || n > 65535 || h == "" {
			return c, errors.New("listeners require an explicit host and port")
		}
	}
	h, _, _ := net.SplitHostPort(c.AdminListen)
	ip := net.ParseIP(h)
	if h != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return c, errors.New("admin listener must bind loopback")
	}
	_, apiPort, _ := net.SplitHostPort(c.Listen)
	_, adminPort, _ := net.SplitHostPort(c.AdminListen)
	if apiPort == adminPort {
		return c, errors.New("API and admin listeners must use separate ports")
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return c, errors.New("invalid data directory")
	}
	c.DataDir = abs
	return c, nil
}

func (c Config) privateDir() string  { return filepath.Join(c.DataDir, "private") }
func (c Config) protocolDir() string { return filepath.Join(c.privateDir(), "protocol") }
