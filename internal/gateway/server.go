package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type App struct {
	cfg                    Config
	backend                Backend
	store                  *store
	mu                     sync.Mutex
	ready, persisted, busy bool
	pairHash               [32]byte
	pairExpiry             time.Time
	jobs                   map[string]*Job
	requestIDs             map[string]string
	done                   map[string]chan struct{}
	now                    func() time.Time
}

func New(c Config, backend Backend) (*App, error) {
	var err error
	c, err = c.Normalized()
	if err != nil {
		return nil, err
	}
	s, err := newStore(c)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: c, backend: backend, store: s, jobs: map[string]*Job{}, requestIDs: map[string]string{}, done: map[string]chan struct{}{}, now: time.Now}
	cookies, present, err := s.loadSession()
	if err != nil {
		return nil, err
	}
	if present {
		backend.UseSession(cookies)
		a.ready, a.persisted = true, true
	}
	backend.SetCookieSink(func(cookies map[string]string) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if validateCookies(cookies) != nil {
			a.ready = false
			return errors.New("session rotation invalid")
		}
		if err := a.store.saveSession(cookies); err != nil {
			a.ready = false
			return errors.New("session persistence failed")
		}
		a.persisted = true
		return nil
	})
	if err := a.loadJobs(); err != nil {
		backend.Close()
		return nil, err
	}
	return a, nil
}

func (a *App) Close() { a.backend.Close() }
func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("operating-system randomness unavailable")
	}
	return hex.EncodeToString(b)
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, code string, unknown bool) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": strings.ReplaceAll(code, "_", " "), "type": code, "code": code, "delivery_unknown": unknown}})
}
func errorDetails(err error) (int, string, bool) {
	var e *CallError
	if errors.As(err, &e) {
		return e.HTTPStatus, e.Code, e.Unknown
	}
	return 502, "upstream_unavailable", true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	if r.Header.Get("Content-Type") != "application/json" && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json;") {
		return errors.New("application/json required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid or unsupported request fields")
	}
	return nil
}

// finishRequestBody gives small request bodies time to arrive before closing
// an HTTP/1 connection. In particular, a client may send headers and body in
// separate packets with Connection: close. Closing an unread body immediately
// can reset that connection before the client receives our rejection response.
// Both bytes and time are bounded; unauthorized uploads are never drained
// indefinitely. Credential input is discarded, not retained or logged.
func finishRequestBody(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	deadlineErr := http.NewResponseController(w).SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	if deadlineErr == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, (64<<10)+1))
	}
	_ = r.Body.Close()
}

func requestHandler(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer finishRequestBody(w, r)
		handler(w, r)
	})
}

func (a *App) APIHandler() http.Handler { return requestHandler(a.api) }
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		apiError(w, 404, "not_found", false)
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(provided), []byte(a.cfg.APIKey)) != 1 {
		apiError(w, 401, "invalid_api_key", false)
		return
	}
	if r.Header.Get("X-Muse-Owner") != "" {
		apiError(w, 400, "unsupported_owner_header", false)
		return
	}
	switch {
	case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
		data := []map[string]string{}
		for _, id := range []string{"muse-default", "muse-image", "muse-video"} {
			data = append(data, map[string]string{"id": id, "object": "model", "owned_by": "muse2api"})
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": data})
	case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
		a.chat(w, r)
	case r.URL.Path == "/v1/images/generations" && r.Method == http.MethodPost:
		a.createMedia(w, r, "image", false)
	case r.URL.Path == "/v1/images/edits" && r.Method == http.MethodPost:
		a.createMedia(w, r, "image", true)
	case r.URL.Path == "/v1/videos" && r.Method == http.MethodPost:
		a.createMedia(w, r, "video", false)
	case strings.HasPrefix(r.URL.Path, "/v1/videos/"):
		a.resource(w, r, "video")
	case strings.HasPrefix(r.URL.Path, "/v1/images/"):
		a.resource(w, r, "image")
	default:
		apiError(w, 404, "not_found", false)
	}
}

func loopbackHost(raw string) bool {
	host := raw
	if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var extensionID = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

func (a *App) adminAllowed(w http.ResponseWriter, r *http.Request, extension bool) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !loopbackHost(host) || !loopbackHost(r.Host) {
		apiError(w, 403, "local_admin_only", false)
		return false
	}
	_, port, err := net.SplitHostPort(r.Host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		apiError(w, 403, "invalid_admin_host", false)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		same := err == nil && u.Scheme == "http" && u.Host == r.Host && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
		ext := extension && err == nil && (u.Scheme == "chrome-extension" || u.Scheme == "moz-extension") && extensionID.MatchString(u.Host) && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
		if !same && !ext {
			apiError(w, 403, "admin_origin_rejected", false)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	return true
}
func (a *App) AdminHandler() http.Handler { return requestHandler(a.admin) }
func (a *App) admin(w http.ResponseWriter, r *http.Request) {
	if !a.adminAllowed(w, r, r.URL.Path == "/admin/browser-session") {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodOptions && r.URL.Path == "/admin/browser-session" {
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(204)
		return
	}
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		_, _ = io.WriteString(w, adminHTML)
	case r.URL.Path == "/admin/status" && r.Method == http.MethodGet:
		a.mu.Lock()
		state := map[string]bool{"ready": a.ready, "persisted": a.persisted, "busy": a.busy}
		a.mu.Unlock()
		writeJSON(w, 200, state)
	case r.URL.Path == "/admin/pair-code" && r.Method == http.MethodPost:
		code := randomID(32)
		a.mu.Lock()
		a.pairHash = sha256.Sum256([]byte(code))
		a.pairExpiry = a.now().Add(2 * time.Minute)
		a.mu.Unlock()
		writeJSON(w, 200, map[string]any{"code": code, "expires_in": 120})
	case r.URL.Path == "/admin/browser-session" && r.Method == http.MethodPost:
		a.importSession(w, r)
	default:
		apiError(w, 404, "not_found", false)
	}
}

func validateCookies(cookies map[string]string) error {
	if len(cookies) == 0 || len(cookies) > 3 || cookies["hatch_sess"] == "" {
		return errors.New("required Muse session missing")
	}
	for key, value := range cookies {
		if key != "hatch_sess" && key != "hatch_native_auth_device" && key != "hatch_vml" {
			return errors.New("unsupported cookie name")
		}
		if len(value) == 0 || len(value) > 16384 || strings.ContainsAny(value, "\r\n\x00; \t") {
			return errors.New("invalid session value")
		}
	}
	return nil
}
func (a *App) importSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code    string            `json:"code"`
		Cookies map[string]string `json:"cookies"`
	}
	if decodeJSON(w, r, &in, 64<<10) != nil {
		apiError(w, 400, "invalid_session_request", false)
		return
	}
	a.mu.Lock()
	hash := sha256.Sum256([]byte(in.Code))
	valid := len(in.Code) == 64 && a.now().Before(a.pairExpiry) && subtle.ConstantTimeCompare(hash[:], a.pairHash[:]) == 1
	if !valid {
		a.mu.Unlock()
		apiError(w, 403, "pair_code_invalid_or_expired", false)
		return
	}
	a.pairExpiry = time.Time{}
	a.pairHash = [32]byte{}
	if a.busy {
		a.mu.Unlock()
		apiError(w, 409, "operation_in_progress", false)
		return
	}
	a.busy = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.busy = false; a.mu.Unlock() }()
	if validateCookies(in.Cookies) != nil {
		apiError(w, 400, "invalid_session_values", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if a.backend.ValidateSession(ctx, in.Cookies) != nil {
		apiError(w, 503, "session_validation_failed", false)
		return
	}
	a.mu.Lock()
	err := a.store.saveSession(in.Cookies)
	if err == nil {
		a.backend.UseSession(in.Cookies)
		a.ready, a.persisted = true, true
	}
	a.mu.Unlock()
	if err != nil {
		apiError(w, 503, "session_persistence_failed", false)
		return
	}
	writeJSON(w, 200, map[string]bool{"ready": true, "persisted": true})
}

func (a *App) begin() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ready {
		return &CallError{"session_required", 503, false}
	}
	if a.busy {
		return &CallError{"operation_in_progress", 409, false}
	}
	a.busy = true
	return nil
}
func (a *App) end() { a.mu.Lock(); a.busy = false; a.mu.Unlock() }

func (a *App) chat(w http.ResponseWriter, r *http.Request) {
	var in chatRequest
	if decodeJSON(w, r, &in, 32<<20) != nil {
		apiError(w, 400, "invalid_or_unsupported_chat_fields", false)
		return
	}
	prompt, refs, err := parseChat(in)
	if err != nil {
		apiError(w, 400, "invalid_chat_input", false)
		return
	}
	if err = a.begin(); err != nil {
		s, c, u := errorDetails(err)
		apiError(w, s, c, u)
		return
	}
	defer a.end()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	id := "chatcmpl-" + randomID(16)
	created := a.now().Unix()
	if !in.Stream {
		text, err := a.backend.Chat(ctx, prompt, refs, nil)
		if err != nil {
			s, c, u := errorDetails(err)
			apiError(w, s, c, u)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": created, "model": "muse-default", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "streaming_unavailable", false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	sent := ""
	emit := func(text string) {
		value := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": "muse-default", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": nil}}}
		data, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	emit("")
	text, err := a.backend.Chat(ctx, prompt, refs, func(delta string) { sent += delta; emit(delta) })
	if err != nil {
		_, code, unknown := errorDetails(err)
		value, _ := json.Marshal(map[string]any{"error": map[string]any{"message": strings.ReplaceAll(code, "_", " "), "type": code, "code": code, "delivery_unknown": unknown}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", value)
		flusher.Flush()
		return
	}
	if sent == "" {
		emit(text)
	} else if strings.HasPrefix(text, sent) && len(text) > len(sent) {
		emit(text[len(sent):])
	} else if text != sent {
		_, _ = io.WriteString(w, "data: {\"error\":{\"code\":\"upstream_stream_changed\",\"delivery_unknown\":true}}\n\n")
		flusher.Flush()
		return
	}
	value, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": "muse-default", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}}})
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", value)
	flusher.Flush()
}

const adminHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Muse2API 本机授权</title><style>body{font:16px system-ui;max-width:760px;margin:48px auto;padding:0 20px;line-height:1.8}code,pre{overflow-wrap:anywhere;white-space:pre-wrap}button{font:inherit;padding:10px}</style><h1>Muse2API 本机授权</h1><p>先在 Muse 官网完成自己的登录，再使用本项目浏览器扩展显式授权必要会话。此页不读取浏览器密码、整份 Profile 或其他网站 Cookie。</p><button id="pair">生成两分钟一次性授权码</button><pre id="code"></pre><p>将授权码填入扩展；扩展只会交给本机管理端口。API Key 不在此页显示。服务器部署请通过 SSH 本地转发访问管理端口。</p><button id="refresh">刷新状态</button><pre id="state"></pre><script>const out=document.querySelector('#code');document.querySelector('#pair').onclick=async()=>{out.textContent='';const r=await fetch('/admin/pair-code',{method:'POST'});const v=await r.json();out.textContent=r.ok?v.code:'无法生成授权码';};async function status(){const r=await fetch('/admin/status');const v=await r.json();document.querySelector('#state').textContent=JSON.stringify(v,null,2)}document.querySelector('#refresh').onclick=status;status();</script></html>`
