package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixtureAPIKey = strings.Repeat("fixture-key-", 4)
const fixtureCookie = "test-only-session-not-valid-upstream"

type fakeBackend struct {
	mu        sync.Mutex
	dir       string
	session   map[string]string
	sink      func(map[string]string) error
	validErr  error
	calls     int
	genErr    error
	gate      chan struct{}
	started   chan struct{}
	chatCalls int
	rotateTo  string
}

func (f *fakeBackend) ValidateSession(_ context.Context, cookies map[string]string) error {
	if f.validErr == nil && f.rotateTo != "" {
		cookies["hatch_sess"] = f.rotateTo
	}
	return f.validErr
}
func (f *fakeBackend) UseSession(c map[string]string)                 { f.session = cloneCookies(c) }
func (f *fakeBackend) SetCookieSink(fn func(map[string]string) error) { f.sink = fn }
func (f *fakeBackend) Chat(_ context.Context, _ string, _ []Reference, delta func(string)) (string, error) {
	f.mu.Lock()
	f.chatCalls++
	f.mu.Unlock()
	if delta != nil {
		delta("hello ")
		delta("world")
	}
	return "hello world", nil
}
func fixturePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func (f *fakeBackend) Generate(ctx context.Context, kind, prompt string, refs []Reference) (Media, error) {
	f.mu.Lock()
	f.calls++
	number := f.calls
	f.mu.Unlock()
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return Media{}, &CallError{"fixture_timeout", 502, true}
		}
	}
	if f.genErr != nil {
		return Media{}, f.genErr
	}
	data := fixturePNG()
	name := strings.Repeat("a", number) + ".png"
	mime := "image/png"
	if kind == "video" {
		name = strings.Repeat("b", number) + ".mp4"
		mime = "video/mp4"
		data = []byte("test-only-registered-video-content")
	}
	if err := os.MkdirAll(f.dir, 0700); err != nil {
		return Media{}, err
	}
	if err := os.WriteFile(filepath.Join(f.dir, name), data, 0600); err != nil {
		return Media{}, err
	}
	return Media{Filename: name, MIME: mime, Bytes: int64(len(data)), Width: 2, Height: 2, Seconds: 10}, nil
}
func (f *fakeBackend) Close()     {}
func (f *fakeBackend) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }
func testConfig(t *testing.T) Config {
	t.Helper()
	c, err := (Config{APIKey: fixtureAPIKey, SessionKey: bytes.Repeat([]byte{7}, 32), DataDir: t.TempDir()}).Normalized()
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func newFixture(t *testing.T) (*App, *fakeBackend) {
	t.Helper()
	c := testConfig(t)
	f := &fakeBackend{dir: filepath.Join(c.protocolDir(), "media")}
	a, err := New(c, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, f
}
func fixtureReady(a *App) { a.mu.Lock(); a.ready = true; a.mu.Unlock() }
func perform(a *App, admin bool, method, path, body, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:49152"
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if admin {
		r.Host = "127.0.0.1:18689"
	} else {
		r.Host = "127.0.0.1:18688"
		r.Header.Set("Authorization", "Bearer "+fixtureAPIKey)
	}
	w := httptest.NewRecorder()
	if admin {
		a.AdminHandler().ServeHTTP(w, r)
	} else {
		a.APIHandler().ServeHTTP(w, r)
	}
	return w
}
func object(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("invalid JSON response: %s", w.Body.String())
	}
	return v
}
func pairCode(t *testing.T, a *App) string {
	t.Helper()
	w := perform(a, true, "POST", "/admin/pair-code", "", "http://127.0.0.1:18689")
	if w.Code != 200 {
		t.Fatalf("pair code status %d", w.Code)
	}
	return object(t, w)["code"].(string)
}
func importCookies(t *testing.T, a *App, code string) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"code": code, "cookies": map[string]string{"hatch_sess": fixtureCookie}})
	return perform(a, true, "POST", "/admin/browser-session", string(data), "chrome-extension://"+strings.Repeat("a", 32))
}
func postMedia(a *App, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+fixtureAPIKey)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	a.APIHandler().ServeHTTP(w, r)
	return w
}
func waitJob(t *testing.T, a *App, id string) *Job {
	t.Helper()
	a.mu.Lock()
	done := a.done[id]
	a.mu.Unlock()
	select {
	case <-done:
		return a.jobSnapshot(id)
	case <-time.After(3 * time.Second):
		t.Fatal("fixture job did not finish")
	}
	return nil
}

func TestConfigurationRejectsUnsafeAdminAndSecrets(t *testing.T) {
	c := testConfig(t)
	for _, admin := range []string{"0.0.0.0:18689", "192.0.2.1:18689", "127.0.0.1:18688"} {
		bad := c
		bad.AdminListen = admin
		if _, err := bad.Normalized(); err == nil {
			t.Fatalf("unsafe admin %s accepted", admin)
		}
	}
	c.APIKey = "short"
	if _, err := c.Normalized(); err == nil {
		t.Fatal("short API key accepted")
	}
}

func TestAdministrationIsSeparateAndOriginProtected(t *testing.T) {
	a, _ := newFixture(t)
	for _, path := range []string{"/", "/admin/status", "/admin/pair-code", "/setup/key"} {
		w := perform(a, false, "GET", path, "", "")
		if w.Code != 404 {
			t.Fatalf("business listener exposed %s", path)
		}
	}
	for _, origin := range []string{"https://example.com", "http://localhost:18689", "null", "chrome-extension://" + strings.Repeat("a", 32)} {
		w := perform(a, true, "POST", "/admin/pair-code", "", origin)
		if w.Code != 403 {
			t.Fatalf("pair origin accepted %q", origin)
		}
	}
	r := httptest.NewRequest("GET", "/admin/status", nil)
	r.Host = "127.0.0.1:18689"
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w := httptest.NewRecorder()
	a.AdminHandler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forwarded admin accepted")
	}
	r = httptest.NewRequest("GET", "/admin/status", nil)
	r.Host = "public.example:18689"
	r.RemoteAddr = "127.0.0.1:1234"
	w = httptest.NewRecorder()
	a.AdminHandler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("external host admin accepted")
	}
	w = perform(a, true, "GET", "/", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), fixtureAPIKey) {
		t.Fatal("admin page missing or disclosed API key")
	}
}

func TestExplicitSessionHandoffEncryptsAndRestores(t *testing.T) {
	a, f := newFixture(t)
	code := pairCode(t, a)
	w := importCookies(t, a, code)
	if w.Code != 200 {
		t.Fatalf("import %d %s", w.Code, w.Body.String())
	}
	if f.session["hatch_sess"] != fixtureCookie {
		t.Fatal("backend did not receive the authorized session")
	}
	data, err := os.ReadFile(filepath.Join(a.store.root, "session.enc"))
	if err != nil || bytes.Contains(data, []byte(fixtureCookie)) {
		t.Fatal("session not encrypted")
	}
	if strings.Contains(w.Body.String(), fixtureCookie) || strings.Contains(w.Body.String(), fixtureAPIKey) {
		t.Fatal("session response disclosed credential")
	}
	if again := importCookies(t, a, code); again.Code != 403 {
		t.Fatal("pair code replay accepted")
	}
	restored, err := New(a.cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if !restored.ready || !restored.persisted {
		t.Fatal("encrypted session not restored")
	}
	bad := a.cfg
	bad.SessionKey = bytes.Repeat([]byte{8}, 32)
	if _, err := New(bad, &fakeBackend{}); err == nil {
		t.Fatal("wrong session key did not fail closed")
	}
}

func TestPairExpiryFailureConsumptionAndBusy(t *testing.T) {
	a, f := newFixture(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	code := pairCode(t, a)
	now = now.Add(121 * time.Second)
	if importCookies(t, a, code).Code != 403 {
		t.Fatal("expired code accepted")
	}
	code = pairCode(t, a)
	f.validErr = errors.New("private diagnostic must not escape")
	w := importCookies(t, a, code)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private diagnostic") {
		t.Fatal("validation error unsafe")
	}
	if importCookies(t, a, code).Code != 403 {
		t.Fatal("failed import did not consume code")
	}
	f.validErr = nil
	code = pairCode(t, a)
	a.busy = true
	if importCookies(t, a, code).Code != 409 {
		t.Fatal("account changed during active operation")
	}
	a.busy = false
}

func TestRejectsUnexpectedCookieAndCrossSiteImport(t *testing.T) {
	a, _ := newFixture(t)
	code := pairCode(t, a)
	payload := `{"code":"` + code + `","cookies":{"hatch_sess":"fixture","unrelated_site":"forbidden"}}`
	w := perform(a, true, "POST", "/admin/browser-session", payload, "")
	if w.Code != 400 {
		t.Fatal("unrelated cookie accepted")
	}
	if importCookies(t, a, code).Code != 403 {
		t.Fatal("invalid cookie did not consume code")
	}
	code = pairCode(t, a)
	payload = `{"code":"` + code + `","cookies":{"hatch_sess":"fixture"}}`
	if perform(a, true, "POST", "/admin/browser-session", payload, "https://muse.ai").Code != 403 {
		t.Fatal("website directly imported a session")
	}
}

func TestAPIAuthenticationOwnerAndChatContract(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	r := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	a.APIHandler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("models exposed without key")
	}
	w = perform(a, false, "GET", "/v1/models", "", "")
	if w.Code != 200 || len(object(t, w)["data"].([]any)) != 3 {
		t.Fatal("model aliases missing")
	}
	r = httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+fixtureAPIKey)
	r.Header.Set("X-Muse-Owner", "other")
	w = httptest.NewRecorder()
	a.APIHandler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("spoofable owner header accepted")
	}
	valid := `{"model":"muse-default","messages":[{"role":"user","content":"hello"}]}`
	w = perform(a, false, "POST", "/v1/chat/completions", valid, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello world") {
		t.Fatal("chat failed")
	}
	for _, body := range []string{`{"model":"muse-default","messages":[{"role":"user","content":"hello"}],"tools":[]}`, `{"model":"muse-default","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://127.0.0.1/secret"}}]}]}`, `{"model":"muse-default","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"x"}}]}]}`} {
		if perform(a, false, "POST", "/v1/chat/completions", body, "").Code != 400 {
			t.Fatal("unsupported input accepted")
		}
	}
	f.mu.Lock()
	n := f.chatCalls
	f.mu.Unlock()
	if n != 1 {
		t.Fatal("invalid input reached backend")
	}
	stream := strings.TrimSuffix(valid, "}") + `,"stream":true}`
	w = perform(a, false, "POST", "/v1/chat/completions", stream, "")
	if w.Code != 200 || !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") || !strings.Contains(w.Body.String(), "chat.completion.chunk") {
		t.Fatal("SSE incomplete")
	}
}

func TestImageValidationAndDataURLInputs(t *testing.T) {
	raw := "data:image/png;base64," + base64.StdEncoding.EncodeToString(fixturePNG())
	if _, err := parseDataImage(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"https://example.com/image.png", "file:///secret", strings.Replace(raw, "image/png", "image/jpeg", 1), "data:image/png;base64,eA=="} {
		if _, err := parseDataImage(bad); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	in := chatRequest{Model: "muse-default", Messages: []chatMessage{{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":"` + raw + `"}}]`)}}}
	prompt, refs, err := parseChat(in)
	if err != nil || len(refs) != 1 || !strings.Contains(prompt, "describe") {
		t.Fatal("image chat parsing failed")
	}
}

func TestVideoIdempotencyBusyAndDownload(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	f.gate = make(chan struct{})
	f.started = make(chan struct{}, 1)
	body := `{"model":"muse-video","prompt":"fixture video","n":1}`
	if postMedia(a, "/v1/videos", body, "").Code != 400 {
		t.Fatal("request ID not required")
	}
	first := postMedia(a, "/v1/videos", body, "fixture-video-1")
	if first.Code != 202 {
		t.Fatalf("create %d", first.Code)
	}
	id := object(t, first)["id"].(string)
	<-f.started
	duplicate := postMedia(a, "/v1/videos", body, "fixture-video-1")
	if object(t, duplicate)["id"] != id || f.count() != 1 {
		t.Fatal("duplicate generated twice")
	}
	if postMedia(a, "/v1/videos", strings.Replace(body, "fixture video", "different", 1), "fixture-video-1").Code != 409 {
		t.Fatal("idempotency conflict accepted")
	}
	if postMedia(a, "/v1/videos", body, "fixture-video-2").Code != 409 {
		t.Fatal("parallel generation accepted")
	}
	close(f.gate)
	j := waitJob(t, a, id)
	if j.Status != "completed" {
		t.Fatal("video not completed")
	}
	w := perform(a, false, "GET", "/v1/videos/"+id+"/content", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "registered-video") {
		t.Fatal("registered content missing")
	}
	if perform(a, false, "POST", "/v1/videos/"+id+"/cancel", "{}", "").Code != 405 {
		t.Fatal("unimplemented cancel appeared supported")
	}
	a.mu.Lock()
	a.jobs[id].Owner = "another-owner"
	a.mu.Unlock()
	if perform(a, false, "GET", "/v1/videos/"+id, "", "").Code != 404 {
		t.Fatal("wrong resource owner accepted")
	}
}

func TestUnknownDeliveryNeverReplaysAndRestartDoesNotSubmit(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	f.genErr = &CallError{"fixture_delivery_unknown", 502, true}
	body := `{"model":"muse-video","prompt":"fixture"}`
	w := postMedia(a, "/v1/videos", body, "fixture-unknown")
	id := object(t, w)["id"].(string)
	if j := waitJob(t, a, id); j.Status != "unknown" || !j.DeliveryUnknown {
		t.Fatal("unknown delivery was not preserved")
	}
	_ = postMedia(a, "/v1/videos", body, "fixture-unknown")
	if f.count() != 1 {
		t.Fatal("unknown was resubmitted")
	}
	restartedFake := &fakeBackend{}
	restarted, err := New(a.cfg, restartedFake)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	again := postMedia(restarted, "/v1/videos", body, "fixture-unknown")
	if object(t, again)["id"] != id || restartedFake.count() != 0 {
		t.Fatal("restart replayed old generation")
	}
}

func TestInterruptedJobsBecomeUnknown(t *testing.T) {
	a, _ := newFixture(t)
	owner := ownerFor(fixtureAPIKey)
	j := &Job{ID: "video-" + strings.Repeat("a", 32), Kind: "video", Owner: owner, RequestID: "fixture-interrupted", Fingerprint: "fixture-hash", Status: "in_progress", CreatedAt: 1}
	if a.saveJob(j) != nil {
		t.Fatal("cannot save interrupted fixture")
	}
	resumed, err := New(a.cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	saved := resumed.jobSnapshot(j.ID)
	if saved.Status != "unknown" || !saved.DeliveryUnknown {
		t.Fatal("restart did not preserve uncertain operation")
	}
}

func TestImagesInlineURLAndEditRoutes(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	body := `{"model":"muse-image","prompt":"fixture image"}`
	w := postMedia(a, "/v1/images/generations", body, "fixture-image-1")
	if w.Code != 200 {
		t.Fatalf("image status %d %s", w.Code, w.Body.String())
	}
	result := object(t, w)
	id := result["id"].(string)
	item := result["data"].([]any)[0].(map[string]any)
	data, err := base64.StdEncoding.DecodeString(item["b64_json"].(string))
	if err != nil || !bytes.Equal(data, fixturePNG()) {
		t.Fatal("image not inline base64")
	}
	_ = postMedia(a, "/v1/images/generations", body, "fixture-image-1")
	if f.count() != 1 {
		t.Fatal("image replayed")
	}
	urlBody := `{"model":"muse-image","prompt":"fixture image","response_format":"url"}`
	urlResult := postMedia(a, "/v1/images/generations", urlBody, "fixture-image-2")
	v := object(t, urlResult)
	link := v["data"].([]any)[0].(map[string]any)["url"].(string)
	if !strings.HasPrefix(link, "/v1/images/") || perform(a, false, "GET", link, "", "").Code != 200 {
		t.Fatal("private image URL missing")
	}
	if perform(a, false, "GET", "/v1/images/"+id, "", "").Code != 200 {
		t.Fatal("image status missing")
	}
	if postMedia(a, "/v1/images/edits", body, "fixture-edit-no-ref").Code != 400 {
		t.Fatal("edit missing reference accepted")
	}
	ref := "data:image/png;base64," + base64.StdEncoding.EncodeToString(fixturePNG())
	editBody := `{"model":"muse-image","prompt":"edit fixture","reference_images":["` + ref + `"]}`
	if postMedia(a, "/v1/images/edits", editBody, "fixture-edit").Code != 200 {
		t.Fatal("JSON image edit failed")
	}
}

func TestNoFakeNativeControlsOrArbitraryFiles(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	for _, extra := range []string{`,"seconds":3`, `,"size":"720p"`, `,"seed":42`, `,"tools":[]`} {
		body := `{"model":"muse-video","prompt":"fixture"` + extra + `}`
		if postMedia(a, "/v1/videos", body, "fixture-control").Code != 400 {
			t.Fatal("unsupported native control silently ignored")
		}
	}
	if f.count() != 0 {
		t.Fatal("invalid native controls submitted")
	}
	for _, path := range []string{"/v1/videos/../session.enc/content", "/v1/images/../../session.enc", "/v1/videos/video-" + strings.Repeat("a", 32) + "/other"} {
		if perform(a, false, "GET", path, "", "").Code == 200 {
			t.Fatal("arbitrary resource path accepted")
		}
	}
}

func TestClientDisconnectDoesNotCancelAcceptedGeneration(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	f.gate = make(chan struct{})
	f.started = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"muse-image","prompt":"fixture detached"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+fixtureAPIKey)
	r.Header.Set("Idempotency-Key", "fixture-disconnected")
	returned := make(chan struct{})
	go func() { a.APIHandler().ServeHTTP(httptest.NewRecorder(), r); close(returned) }()
	<-f.started
	cancel()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("caller did not detach")
	}
	close(f.gate)
	a.mu.Lock()
	id := a.requestIDs[ownerFor(fixtureAPIKey)+":fixture-disconnected"]
	a.mu.Unlock()
	if waitJob(t, a, id).Status != "completed" || f.count() != 1 {
		t.Fatal("accepted worker was canceled or repeated")
	}
}

func TestForwardedLoopbackPortSupportsExplicitHandoff(t *testing.T) {
	a, _ := newFixture(t)
	r := httptest.NewRequest("POST", "http://127.0.0.1:28689/admin/pair-code", nil)
	r.Host = "127.0.0.1:28689"
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Origin", "http://127.0.0.1:28689")
	w := httptest.NewRecorder()
	a.AdminHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("SSH local forwarding rejected: %d", w.Code)
	}
	code := object(t, w)["code"].(string)
	data, _ := json.Marshal(map[string]any{"code": code, "cookies": map[string]string{"hatch_sess": fixtureCookie}})
	r = httptest.NewRequest("POST", "http://127.0.0.1:28689/admin/browser-session", bytes.NewReader(data))
	r.Host = "127.0.0.1:28689"
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "chrome-extension://"+strings.Repeat("b", 32))
	w = httptest.NewRecorder()
	a.AdminHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("forwarded import rejected: %d", w.Code)
	}
}

func TestValidationCookieRotationIsSavedOnlyAfterSuccess(t *testing.T) {
	a, f := newFixture(t)
	f.rotateTo = "test-only-rotated-session"
	if importCookies(t, a, pairCode(t, a)).Code != 200 {
		t.Fatal("rotating import failed")
	}
	loaded, present, err := a.store.loadSession()
	if err != nil || !present || loaded["hatch_sess"] != f.rotateTo || f.session["hatch_sess"] != f.rotateTo {
		t.Fatal("validated cookie rotation was lost")
	}
	f.validErr = errors.New("fixture validation failed")
	f.rotateTo = "test-only-rejected-session"
	if importCookies(t, a, pairCode(t, a)).Code != 503 {
		t.Fatal("bad replacement accepted")
	}
	loaded, _, err = a.store.loadSession()
	if err != nil || loaded["hatch_sess"] != "test-only-rotated-session" {
		t.Fatal("failed validation replaced active encrypted session")
	}
}

func TestIdempotencyKeyTakesPrecedenceOverTraceHeader(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	f.genErr = &CallError{"fixture_delivery_unknown", 502, true}
	body := `{"model":"muse-video","prompt":"trace fixture"}`
	request := func(trace string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+fixtureAPIKey)
		r.Header.Set("Idempotency-Key", "fixture-stable-idempotency")
		r.Header.Set("X-Request-ID", trace)
		w := httptest.NewRecorder()
		a.APIHandler().ServeHTTP(w, r)
		return w
	}
	w := request("fixture-trace-1")
	if w.Code != 200 && w.Code != 202 {
		t.Fatalf("independent trace rejected: %d", w.Code)
	}
	id := object(t, w)["id"].(string)
	waitJob(t, a, id)
	again := request("fixture-trace-2")
	if object(t, again)["id"] != id || f.count() != 1 {
		t.Fatal("changed trace bypassed stable idempotency")
	}
}

func TestPersistenceFailurePreventsSubmission(t *testing.T) {
	a, f := newFixture(t)
	fixtureReady(a)
	dir := filepath.Join(a.store.root, "jobs")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("test-only-blocked-storage"), 0600); err != nil {
		t.Fatal(err)
	}
	w := postMedia(a, "/v1/videos", `{"model":"muse-video","prompt":"fixture"}`, "fixture-storage-failure")
	if w.Code != 503 || f.count() != 0 {
		t.Fatal("generation was submitted before checkpoint persistence")
	}
}
