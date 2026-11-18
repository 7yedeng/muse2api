package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Job struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Owner           string `json:"owner"`
	RequestID       string `json:"request_id"`
	Fingerprint     string `json:"fingerprint"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"created_at"`
	CompletedAt     int64  `json:"completed_at,omitempty"`
	Media           *Media `json:"media,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	ErrorStatus     int    `json:"error_status,omitempty"`
	DeliveryUnknown bool   `json:"delivery_unknown,omitempty"`
}

var validID = regexp.MustCompile(`^(image|video)-[a-f0-9]{32}$`)
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var validMediaName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ownerFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
func (a *App) jobPath(id string) string { return filepath.Join(a.store.root, "jobs", id+".json") }
func (a *App) saveJob(job *Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return atomicWrite(a.jobPath(job.ID), data)
}
func (a *App) loadJobs() error {
	dir := filepath.Join(a.store.root, "jobs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create job storage")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("cannot read job storage")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID.MatchString(id) {
			return errors.New("invalid job storage filename")
		}
		data, err := os.ReadFile(a.jobPath(id))
		if err != nil || len(data) > 64<<10 {
			return errors.New("cannot read job record")
		}
		var job Job
		if json.Unmarshal(data, &job) != nil || job.ID != id || job.Owner == "" || job.Fingerprint == "" || !validRequestID.MatchString(job.RequestID) || (job.Kind != "image" && job.Kind != "video") || !strings.HasPrefix(job.ID, job.Kind+"-") {
			return errors.New("invalid job record")
		}
		switch job.Status {
		case "queued", "in_progress":
			job.Status = "unknown"
			job.ErrorCode = "interrupted_delivery_unknown"
			job.ErrorStatus = 502
			job.DeliveryUnknown = true
			if a.saveJob(&job) != nil {
				return errors.New("cannot preserve interrupted job")
			}
		case "completed", "failed", "unknown":
		default:
			return errors.New("invalid job state")
		}
		key := job.Owner + ":" + job.RequestID
		if _, exists := a.requestIDs[key]; exists {
			return errors.New("duplicate idempotency record")
		}
		a.requestIDs[key] = id
		a.jobs[id] = &job
		done := make(chan struct{})
		close(done)
		a.done[id] = done
	}
	return nil
}
func mediaInput(r *http.Request, w http.ResponseWriter, kind string, edit bool) (generationRequest, []Reference, error) {
	var in generationRequest
	if decodeJSON(w, r, &in, 32<<20) != nil {
		return in, nil, errors.New("invalid or unsupported media fields")
	}
	expected := "muse-image"
	if kind == "video" {
		expected = "muse-video"
	}
	if in.Model != expected || strings.TrimSpace(in.Prompt) == "" || len(in.Prompt) > 128<<10 {
		return in, nil, errors.New("invalid model or prompt")
	}
	if in.N == nil {
		n := 1
		in.N = &n
	}
	if *in.N != 1 {
		return in, nil, errors.New("only n=1 is supported")
	}
	if kind == "video" && in.ResponseFormat != "" {
		return in, nil, errors.New("video response_format is unsupported")
	}
	if kind == "image" {
		if in.ResponseFormat == "" {
			in.ResponseFormat = "b64_json"
		}
		if in.ResponseFormat != "b64_json" && in.ResponseFormat != "url" {
			return in, nil, errors.New("unsupported response_format")
		}
	}
	refs, err := parseReferences(in.References)
	if err != nil {
		return in, nil, err
	}
	if edit && len(refs) == 0 {
		return in, nil, errors.New("image edits require a reference image")
	}
	return in, refs, nil
}
func (a *App) createMedia(w http.ResponseWriter, r *http.Request, kind string, edit bool) {
	in, refs, err := mediaInput(r, w, kind, edit)
	if err != nil {
		apiError(w, 400, "invalid_or_unsupported_media_input", false)
		return
	}
	requestID := r.Header.Get("Idempotency-Key")
	legacy := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = legacy
	}
	if !validRequestID.MatchString(requestID) {
		apiError(w, 400, "valid_request_id_required", false)
		return
	}
	encoded, _ := json.Marshal(in)
	op := kind
	if edit {
		op += "-edit"
	}
	fingerprint := sha256.Sum256(append([]byte(op+":"), encoded...))
	hash := hex.EncodeToString(fingerprint[:])
	owner := ownerFor(a.cfg.APIKey)
	key := owner + ":" + requestID
	a.mu.Lock()
	if id, exists := a.requestIDs[key]; exists {
		job := a.jobs[id]
		if job.Fingerprint != hash {
			a.mu.Unlock()
			apiError(w, 409, "idempotency_conflict", false)
			return
		}
		done := a.done[id]
		a.mu.Unlock()
		a.respondToJob(w, r, id, kind, in.ResponseFormat, done)
		return
	}
	if !a.ready {
		a.mu.Unlock()
		apiError(w, 503, "session_required", false)
		return
	}
	if a.busy {
		a.mu.Unlock()
		apiError(w, 409, "operation_in_progress", false)
		return
	}
	job := &Job{ID: kind + "-" + randomID(16), Kind: kind, Owner: owner, RequestID: requestID, Fingerprint: hash, Status: "queued", CreatedAt: a.now().Unix()}
	if a.saveJob(job) != nil {
		a.mu.Unlock()
		apiError(w, 503, "job_persistence_failed", false)
		return
	}
	a.busy = true
	a.jobs[job.ID] = job
	a.requestIDs[key] = job.ID
	done := make(chan struct{})
	a.done[job.ID] = done
	id := job.ID
	a.mu.Unlock()
	go a.runJob(id, in.Prompt, refs)
	a.respondToJob(w, r, id, kind, in.ResponseFormat, done)
}
func (a *App) runJob(id, prompt string, refs []Reference) {
	a.mu.Lock()
	job := a.jobs[id]
	job.Status = "in_progress"
	if a.saveJob(job) != nil {
		job.Status = "failed"
		job.ErrorCode = "job_persistence_failed"
		job.ErrorStatus = 503
		a.busy = false
		close(a.done[id])
		a.mu.Unlock()
		return
	}
	kind := job.Kind
	a.mu.Unlock()
	// The worker owns its lifetime. A disconnected HTTP caller does not cancel
	// the accepted native request or cause another submission.
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.GenerateTimeout)
	defer cancel()
	media, err := a.backend.Generate(ctx, kind, prompt, refs)
	if err == nil {
		err = a.validateMedia(media, kind)
		if err == nil {
			media.MIME, _, _ = mime.ParseMediaType(media.MIME)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	job = a.jobs[id]
	if err != nil {
		status, code, unknown := errorDetails(err)
		job.Status = "failed"
		if unknown {
			job.Status = "unknown"
		}
		job.ErrorCode, job.ErrorStatus, job.DeliveryUnknown = code, status, unknown
	} else {
		job.Status = "completed"
		job.Media = &media
		job.CompletedAt = a.now().Unix()
	}
	if a.saveJob(job) != nil {
		job.Status = "unknown"
		job.ErrorCode = "job_persistence_failed"
		job.ErrorStatus = 503
		job.DeliveryUnknown = true
	}
	a.busy = false
	close(a.done[id])
}
func (a *App) validateMedia(m Media, kind string) error {
	if !validMediaName.MatchString(m.Filename) || strings.Contains(m.Filename, "..") || filepath.Base(m.Filename) != m.Filename {
		return &CallError{"invalid_upstream_media", 502, true}
	}
	base, _, err := mime.ParseMediaType(m.MIME)
	if err != nil {
		return &CallError{"invalid_upstream_media", 502, true}
	}
	if kind == "image" {
		switch base {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			return &CallError{"invalid_upstream_media", 502, true}
		}
	} else if base != "video/mp4" && base != "video/webm" {
		return &CallError{"invalid_upstream_media", 502, true}
	}
	path := filepath.Join(a.cfg.protocolDir(), "media", m.Filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != m.Bytes || m.Bytes <= 0 {
		return &CallError{"upstream_media_file_unavailable", 502, true}
	}
	return nil
}
func (a *App) jobSnapshot(id string) *Job {
	a.mu.Lock()
	defer a.mu.Unlock()
	j := a.jobs[id]
	if j == nil {
		return nil
	}
	copy := *j
	if j.Media != nil {
		m := *j.Media
		copy.Media = &m
	}
	return &copy
}
func publicJob(j *Job) map[string]any {
	model := "muse-image"
	if j.Kind == "video" {
		model = "muse-video"
	}
	v := map[string]any{"id": j.ID, "object": j.Kind, "model": model, "status": j.Status, "created_at": j.CreatedAt, "progress": 0}
	if j.Status == "completed" && j.Media != nil {
		v["progress"] = 100
		v["completed_at"] = j.CompletedAt
		v["width"] = j.Media.Width
		v["height"] = j.Media.Height
		v["seconds"] = j.Media.Seconds
		v["url"] = "/v1/" + j.Kind + "s/" + j.ID + "/content"
	}
	if j.ErrorCode != "" {
		v["error"] = map[string]any{"code": j.ErrorCode, "message": strings.ReplaceAll(j.ErrorCode, "_", " "), "delivery_unknown": j.DeliveryUnknown}
	}
	return v
}
func (a *App) respondToJob(w http.ResponseWriter, r *http.Request, id, kind, format string, done <-chan struct{}) {
	if kind == "video" {
		j := a.jobSnapshot(id)
		status := 202
		if j.Status == "completed" || j.Status == "unknown" || j.Status == "failed" {
			status = 200
		}
		writeJSON(w, status, publicJob(j))
		return
	}
	select {
	case <-r.Context().Done():
		return
	case <-done:
	}
	j := a.jobSnapshot(id)
	if j.Status != "completed" || j.Media == nil {
		status := j.ErrorStatus
		if status < 400 {
			status = 502
		}
		writeJSON(w, status, map[string]any{"id": j.ID, "error": map[string]any{"message": strings.ReplaceAll(j.ErrorCode, "_", " "), "type": j.ErrorCode, "code": j.ErrorCode, "delivery_unknown": j.DeliveryUnknown}})
		return
	}
	item := map[string]string{}
	if format == "url" {
		item["url"] = "/v1/images/" + id + "/content"
	} else {
		if j.Media.Bytes > 32<<20 {
			writeJSON(w, 413, map[string]any{"id": j.ID, "error": map[string]any{"code": "image_too_large_for_inline_response", "type": "image_too_large_for_inline_response", "message": "image too large for inline response; use the resource content endpoint", "delivery_unknown": false}})
			return
		}
		data, err := os.ReadFile(filepath.Join(a.cfg.protocolDir(), "media", j.Media.Filename))
		if err != nil {
			writeJSON(w, 503, map[string]any{"id": j.ID, "error": map[string]any{"code": "media_file_unavailable", "type": "media_file_unavailable", "message": "media file unavailable", "delivery_unknown": false}})
			return
		}
		item["b64_json"] = base64.StdEncoding.EncodeToString(data)
	}
	writeJSON(w, 200, map[string]any{"id": id, "created": j.CreatedAt, "data": []any{item}})
}
func (a *App) resource(w http.ResponseWriter, r *http.Request, kind string) {
	if r.Method != http.MethodGet {
		apiError(w, 405, "method_not_allowed", false)
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, "/v1/"+kind+"s/")
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || len(parts) > 2 || !validID.MatchString(parts[0]) || !strings.HasPrefix(parts[0], kind+"-") || (len(parts) == 2 && parts[1] != "content") {
		apiError(w, 404, "not_found", false)
		return
	}
	j := a.jobSnapshot(parts[0])
	if j == nil || j.Kind != kind || j.Owner != ownerFor(a.cfg.APIKey) {
		apiError(w, 404, "not_found", false)
		return
	}
	if len(parts) == 1 {
		writeJSON(w, 200, publicJob(j))
		return
	}
	if j.Status != "completed" || j.Media == nil {
		apiError(w, 409, "media_not_completed", j.DeliveryUnknown)
		return
	}
	if a.validateMedia(*j.Media, kind) != nil {
		apiError(w, 503, "media_file_unavailable", false)
		return
	}
	path := filepath.Join(a.cfg.protocolDir(), "media", j.Media.Filename)
	file, err := os.Open(path)
	if err != nil {
		apiError(w, 503, "media_file_unavailable", false)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		apiError(w, 503, "media_file_unavailable", false)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(j.Media.MIME)
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, j.Media.Filename, info.ModTime(), file)
}
