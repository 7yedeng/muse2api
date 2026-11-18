package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
)

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream,omitempty"`
}
type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}
type generationRequest struct {
	Model          string   `json:"model"`
	Prompt         string   `json:"prompt"`
	N              *int     `json:"n,omitempty"`
	References     []string `json:"reference_images,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
}

func parseDataImage(raw string) (Reference, error) {
	if !strings.HasPrefix(raw, "data:") {
		return Reference{}, errors.New("only image data URLs are supported")
	}
	header, value, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok {
		return Reference{}, errors.New("invalid data URL")
	}
	mime := strings.TrimSuffix(header, ";base64")
	if header == mime {
		return Reference{}, errors.New("base64 image required")
	}
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return Reference{}, errors.New("unsupported image type")
	}
	if len(value) > 14<<20 {
		return Reference{}, errors.New("image too large")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) == 0 || len(data) > 10<<20 {
		return Reference{}, errors.New("invalid image data")
	}
	actual := http.DetectContentType(data)
	if actual != mime {
		return Reference{}, errors.New("image MIME mismatch")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 64_000_000 {
		return Reference{}, errors.New("invalid or oversized image dimensions")
	}
	return Reference{Data: data, MIME: mime}, nil
}
func parseReferences(raw []string) ([]Reference, error) {
	if len(raw) > 4 {
		return nil, errors.New("too many images")
	}
	out := []Reference{}
	total := 0
	for _, value := range raw {
		r, err := parseDataImage(value)
		if err != nil {
			return nil, err
		}
		total += len(r.Data)
		out = append(out, r)
	}
	if total > 24<<20 {
		return nil, errors.New("images exceed total limit")
	}
	return out, nil
}
func parseChat(in chatRequest) (string, []Reference, error) {
	if in.Model != "muse-default" || len(in.Messages) == 0 || len(in.Messages) > 100 {
		return "", nil, errors.New("invalid model or messages")
	}
	var prompt strings.Builder
	refs := []Reference{}
	total := 0
	hasUser := false
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer", "user", "assistant":
		default:
			return "", nil, errors.New("unsupported role")
		}
		if m.Role == "user" {
			hasUser = true
		}
		text := ""
		if json.Unmarshal(m.Content, &text) != nil {
			var parts []json.RawMessage
			if json.Unmarshal(m.Content, &parts) != nil || len(parts) > 8 {
				return "", nil, errors.New("invalid content")
			}
			for _, raw := range parts {
				var p contentPart
				d := json.NewDecoder(bytes.NewReader(raw))
				d.DisallowUnknownFields()
				if d.Decode(&p) != nil {
					return "", nil, errors.New("unsupported content fields")
				}
				switch p.Type {
				case "text":
					if p.ImageURL != nil {
						return "", nil, errors.New("invalid text part")
					}
					text += p.Text
				case "image_url":
					if p.ImageURL == nil || p.Text != "" {
						return "", nil, errors.New("invalid image part")
					}
					r, err := parseDataImage(p.ImageURL.URL)
					if err != nil {
						return "", nil, err
					}
					refs = append(refs, r)
					total += len(r.Data)
				default:
					return "", nil, errors.New("unsupported content type")
				}
			}
		}
		if len(text) > 256<<10 {
			return "", nil, errors.New("message too long")
		}
		fmt.Fprintf(&prompt, "[%s]\n%s\n", m.Role, text)
	}
	if !hasUser || len(refs) > 4 || total > 24<<20 || prompt.Len() > 512<<10 {
		return "", nil, errors.New("chat input limit exceeded")
	}
	return prompt.String(), refs, nil
}
