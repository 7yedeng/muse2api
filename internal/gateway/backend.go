package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/CoolkHz/muse-protocol-go/muse"
)

type Reference struct {
	Data []byte
	MIME string
}
type Media struct {
	Filename string  `json:"filename"`
	MIME     string  `json:"mime"`
	Bytes    int64   `json:"bytes"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
}
type Backend interface {
	ValidateSession(context.Context, map[string]string) error
	UseSession(map[string]string)
	SetCookieSink(func(map[string]string) error)
	Chat(context.Context, string, []Reference, func(string)) (string, error)
	Generate(context.Context, string, string, []Reference) (Media, error)
	Close()
}
type CallError struct {
	Code       string
	HTTPStatus int
	Unknown    bool
}

func (e *CallError) Error() string { return e.Code }

type SDKBackend struct {
	cfg        muse.Config
	mu         sync.Mutex
	client     *muse.ProtoEngine
	account    *muse.Account
	sink       func(map[string]string) error
	persistErr bool
}

func NewSDKBackend(c Config) *SDKBackend {
	return &SDKBackend{cfg: muse.Config{DataDir: c.protocolDir(), Proxy: c.ProxyURL, ApprovalPolicy: "deny", ChatTimeout: 3 * time.Minute, GenerateTimeout: c.GenerateTimeout}}
}
func cloneCookies(m map[string]string) map[string]string {
	o := map[string]string{}
	for k, v := range m {
		o[k] = v
	}
	return o
}
func (b *SDKBackend) ValidateSession(ctx context.Context, cookies map[string]string) error {
	client := muse.NewClient(b.cfg)
	defer client.Close()
	var capturedMu sync.Mutex
	captured := cloneCookies(cookies)
	client.SetHooks(func(_ string, rotated map[string]string, _ map[string]int64) {
		next := map[string]string{}
		for _, name := range []string{"hatch_sess", "hatch_native_auth_device", "hatch_vml"} {
			if value := rotated[name]; value != "" {
				next[name] = value
			}
		}
		capturedMu.Lock()
		captured = next
		capturedMu.Unlock()
	}, nil)
	a := &muse.Account{ID: "validation", Cookies: cloneCookies(cookies), Proxy: b.cfg.Proxy}
	r, err := client.Diagnose(ctx, a)
	if err != nil {
		return &CallError{"session_validation_failed", 503, false}
	}
	if _, exists := r["error"]; exists || r["vm_assigned"] != true {
		return &CallError{"session_validation_failed", 503, false}
	}
	for _, key := range []string{"model", "sessions"} {
		item, ok := r[key].(map[string]any)
		if !ok || item["status"] != 200 {
			return &CallError{"session_validation_failed", 503, false}
		}
	}
	capturedMu.Lock()
	latest := cloneCookies(captured)
	capturedMu.Unlock()
	if validateCookies(latest) != nil {
		return &CallError{"session_validation_failed", 503, false}
	}
	// Only the caller's not-yet-committed import map is updated. This temporary
	// validator never writes or replaces the currently active persisted session.
	for key := range cookies {
		delete(cookies, key)
	}
	for key, value := range latest {
		cookies[key] = value
	}
	return nil
}
func (b *SDKBackend) SetCookieSink(fn func(map[string]string) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sink = fn
}
func (b *SDKBackend) UseSession(cookies map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		b.client.Close()
	}
	b.persistErr = false
	b.account = &muse.Account{ID: "primary", Cookies: cloneCookies(cookies), Proxy: b.cfg.Proxy}
	b.client = muse.NewClient(b.cfg)
	b.client.SetHooks(func(_ string, rotated map[string]string, expiry map[string]int64) {
		b.mu.Lock()
		filtered := map[string]string{}
		for _, key := range []string{"hatch_sess", "hatch_native_auth_device", "hatch_vml"} {
			if value := rotated[key]; value != "" {
				filtered[key] = value
			}
		}
		b.account = &muse.Account{ID: "primary", Cookies: cloneCookies(filtered), CookieExpires: expiry, Proxy: b.cfg.Proxy}
		sink := b.sink
		b.mu.Unlock()
		if sink != nil && sink(filtered) != nil {
			b.mu.Lock()
			b.persistErr = true
			b.mu.Unlock()
		}
	}, nil)
}
func (b *SDKBackend) state() (*muse.ProtoEngine, *muse.Account, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.persistErr {
		return nil, nil, &CallError{"session_persistence_failed", 503, true}
	}
	if b.client == nil || b.account == nil {
		return nil, nil, &CallError{"session_required", 503, false}
	}
	return b.client, b.account, nil
}
func sdkRefs(refs []Reference) []muse.RefImage {
	out := make([]muse.RefImage, len(refs))
	for i, r := range refs {
		out[i] = muse.RefImage{Data: r.Data, Mime: r.MIME}
	}
	return out
}
func safeSDKError(err error) error {
	if err == nil {
		return nil
	}
	var uncertain *muse.NoRetry
	unknown := errors.As(err, &uncertain)
	code, status := "upstream_unavailable", 502
	switch muse.KindOf(err) {
	case muse.KindAuth:
		code, status = "upstream_session_expired", 503
	case muse.KindQuota:
		code, status = "upstream_quota_exhausted", 429
	case muse.KindRefusal:
		code, status = "upstream_refused", 422
	case muse.KindBadRequest:
		code, status = "upstream_invalid_request", 400
	case muse.KindRateLimit:
		code, status = "upstream_rate_limited", 429
	default:
		unknown = true
	}
	return &CallError{code, status, unknown}
}
func (b *SDKBackend) Chat(ctx context.Context, prompt string, refs []Reference, delta func(string)) (string, error) {
	client, a, err := b.state()
	if err != nil {
		return "", err
	}
	r, err := client.Chat(ctx, a, prompt, sdkRefs(refs), delta)
	if err != nil {
		return "", safeSDKError(err)
	}
	if r == nil {
		return "", &CallError{"empty_upstream_reply", 502, true}
	}
	return r.Text, nil
}
func (b *SDKBackend) Generate(ctx context.Context, kind, prompt string, refs []Reference) (Media, error) {
	client, a, err := b.state()
	if err != nil {
		return Media{}, err
	}
	items, err := client.Generate(ctx, a, muse.GenRequest{Kind: kind, Prompt: prompt, Refs: sdkRefs(refs), N: 1})
	if err != nil {
		return Media{}, safeSDKError(err)
	}
	if len(items) != 1 {
		return Media{}, &CallError{"upstream_media_missing", 502, true}
	}
	m := items[0]
	return Media{m.Filename, m.Mime, m.Bytes, m.Width, m.Height, m.Seconds}, nil
}
func (b *SDKBackend) Close() {
	b.mu.Lock()
	client := b.client
	b.client = nil
	b.account = nil
	b.mu.Unlock()
	if client != nil {
		client.Close()
	}
}
