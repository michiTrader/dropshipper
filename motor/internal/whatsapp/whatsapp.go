// Package whatsapp talks to the official WhatsApp Business Cloud API (Meta).
//
// Official API only: unofficial libraries (Baileys, whatsapp-web.js) get
// business numbers banned, which is the one failure this business cannot
// afford. The Cloud API needs a dedicated number registered in Meta Business.
package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client sends messages through the Cloud API.
type Client struct {
	Token         string
	PhoneNumberID string
	GraphVersion  string
	BaseURL       string // default https://graph.facebook.com (overridable in tests)
	HTTP          *http.Client
}

// New builds a client.
func New(token, phoneNumberID, version string) *Client {
	if version == "" {
		version = "v21.0"
	}
	return &Client{Token: token, PhoneNumberID: phoneNumberID, GraphVersion: version,
		BaseURL: "https://graph.facebook.com", HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) endpoint(path string) string {
	return strings.TrimRight(c.BaseURL, "/") + "/" + c.GraphVersion + "/" + strings.TrimLeft(path, "/")
}

// ErrNotConfigured means no WhatsApp credentials are set.
var ErrNotConfigured = errors.New("WhatsApp no está configurado: faltan el token y el ID del número")

// PhoneInfo describes the business number behind the credentials.
type PhoneInfo struct {
	DisplayPhone string `json:"numero"`
	VerifiedName string `json:"nombre_verificado"`
	Quality      string `json:"calidad"`
}

// Check proves token + phone number ID work by reading the number's profile.
func (c *Client) Check(ctx context.Context) (PhoneInfo, error) {
	if c == nil || c.Token == "" || c.PhoneNumberID == "" {
		return PhoneInfo{}, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.endpoint(c.PhoneNumberID)+"?fields=display_phone_number,verified_name,quality_rating", nil)
	if err != nil {
		return PhoneInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return PhoneInfo{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return PhoneInfo{}, fmt.Errorf("WhatsApp HTTP %d: %s", resp.StatusCode, apiError(raw))
	}
	var out struct {
		Display string `json:"display_phone_number"`
		Name    string `json:"verified_name"`
		Quality string `json:"quality_rating"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return PhoneInfo{}, fmt.Errorf("respuesta de WhatsApp inválida: %w", err)
	}
	return PhoneInfo{DisplayPhone: out.Display, VerifiedName: out.Name, Quality: out.Quality}, nil
}

// SendText sends a free-form text message (allowed inside the 24 h customer
// service window). Returns the WhatsApp message id.
func (c *Client) SendText(ctx context.Context, to, text string) (string, error) {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text":              map[string]any{"preview_url": true, "body": text},
	}
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.post(ctx, c.PhoneNumberID+"/messages", payload, &out); err != nil {
		return "", err
	}
	if len(out.Messages) == 0 {
		return "", errors.New("WhatsApp no devolvió id de mensaje")
	}
	return out.Messages[0].ID, nil
}

// MarkRead marks an inbound message as read (blue ticks) and optionally shows
// the "escribiendo…" indicator while the AI prepares the answer.
func (c *Client) MarkRead(ctx context.Context, waID string, typing bool) error {
	payload := map[string]any{"messaging_product": "whatsapp", "status": "read", "message_id": waID}
	if typing {
		payload["typing_indicator"] = map[string]any{"type": "text"}
	}
	return c.post(ctx, c.PhoneNumberID+"/messages", payload, nil)
}

func (c *Client) post(ctx context.Context, path string, payload any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(path), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("WhatsApp HTTP %d: %s", resp.StatusCode, apiError(raw))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func apiError(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg := fmt.Sprintf("%s (código %d)", e.Error.Message, e.Error.Code)
		if e.Error.Code == 131047 {
			msg += ": pasaron más de 24 h desde el último mensaje del cliente; WhatsApp solo permite plantillas aprobadas"
		}
		return msg
	}
	s := string(raw)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// DownloadMedia fetches an inbound media file (image/audio/document) and
// saves it under dir. Returns the saved file name.
func (c *Client) DownloadMedia(ctx context.Context, mediaID, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(mediaID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	var meta struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("media %s: HTTP %d: %s", mediaID, resp.StatusCode, apiError(raw))
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.URL == "" {
		return "", fmt.Errorf("media %s sin url", mediaID)
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err = c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("descargar media %s: HTTP %d", mediaID, resp.StatusCode)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := SafeName(mediaID) + ExtFor(meta.MimeType)
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 25<<20)); err != nil {
		return "", err
	}
	return name, nil
}

// SafeName keeps only characters safe for a file name.
func SafeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return b.String()
}

// ExtFor maps a MIME type to a file extension.
func ExtFor(mime string) string {
	switch {
	case strings.Contains(mime, "jpeg"), strings.Contains(mime, "jpg"):
		return ".jpg"
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "pdf"):
		return ".pdf"
	case strings.Contains(mime, "ogg"):
		return ".ogg"
	case strings.Contains(mime, "mp4"):
		return ".mp4"
	}
	return ".bin"
}

// VerifySignature checks Meta's X-Hub-Signature-256 header (HMAC-SHA256 of
// the raw body with the App Secret). Empty secret = verification disabled.
func VerifySignature(appSecret string, body []byte, header string) bool {
	if appSecret == "" {
		return true
	}
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// ---------- webhook payload ----------

// IncomingMessage is a parsed inbound message.
type IncomingMessage struct {
	From      string
	Name      string
	ID        string
	Timestamp time.Time
	Type      string
	Text      string
	MediaID   string
	MimeType  string
}

// StatusUpdate is a delivery receipt (sent, delivered, read, failed).
type StatusUpdate struct {
	ID     string
	Status string
	Error  string
}

type webhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Contacts []struct {
					WaID    string `json:"wa_id"`
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
				} `json:"contacts"`
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      struct {
						Body string `json:"body"`
					} `json:"text"`
					Image    mediaObj `json:"image"`
					Audio    mediaObj `json:"audio"`
					Video    mediaObj `json:"video"`
					Document mediaObj `json:"document"`
					Sticker  mediaObj `json:"sticker"`
					Button   struct {
						Text string `json:"text"`
					} `json:"button"`
					Interactive struct {
						ButtonReply struct {
							Title string `json:"title"`
						} `json:"button_reply"`
						ListReply struct {
							Title string `json:"title"`
						} `json:"list_reply"`
					} `json:"interactive"`
					Location struct {
						Latitude  float64 `json:"latitude"`
						Longitude float64 `json:"longitude"`
						Name      string  `json:"name"`
						Address   string  `json:"address"`
					} `json:"location"`
					Referral struct {
						Headline string `json:"headline"`
						Body     string `json:"body"`
					} `json:"referral"`
				} `json:"messages"`
				Statuses []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Errors []struct {
						Title string `json:"title"`
					} `json:"errors"`
				} `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type mediaObj struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
}

// ParseWebhook extracts messages and status updates from a webhook body.
func ParseWebhook(body []byte) ([]IncomingMessage, []StatusUpdate, error) {
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, nil, err
	}
	var msgs []IncomingMessage
	var stats []StatusUpdate
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			v := ch.Value
			names := map[string]string{}
			for _, c := range v.Contacts {
				names[c.WaID] = c.Profile.Name
			}
			for _, m := range v.Messages {
				im := IncomingMessage{From: m.From, Name: names[m.From], ID: m.ID, Type: m.Type}
				if ts, err := strconv.ParseInt(m.Timestamp, 10, 64); err == nil {
					im.Timestamp = time.Unix(ts, 0).UTC()
				}
				switch m.Type {
				case "text":
					im.Text = m.Text.Body
				case "image":
					im.MediaID, im.MimeType, im.Text = m.Image.ID, m.Image.MimeType, m.Image.Caption
				case "audio":
					im.MediaID, im.MimeType = m.Audio.ID, m.Audio.MimeType
				case "video":
					im.MediaID, im.MimeType, im.Text = m.Video.ID, m.Video.MimeType, m.Video.Caption
				case "document":
					im.MediaID, im.MimeType, im.Text = m.Document.ID, m.Document.MimeType, strings.TrimSpace(m.Document.Caption+" "+m.Document.Filename)
				case "sticker":
					im.MediaID, im.MimeType = m.Sticker.ID, m.Sticker.MimeType
				case "button":
					im.Type, im.Text = "text", m.Button.Text
				case "interactive":
					im.Type, im.Text = "text", m.Interactive.ButtonReply.Title+m.Interactive.ListReply.Title
				case "location":
					im.Text = strings.TrimSpace(fmt.Sprintf("[Ubicación] %s %s (%.6f, %.6f)", m.Location.Name, m.Location.Address, m.Location.Latitude, m.Location.Longitude))
				default:
					im.Text = "[mensaje de tipo " + m.Type + "]"
				}
				if m.Referral.Headline != "" || m.Referral.Body != "" {
					im.Text = strings.TrimSpace(im.Text + "\n[Llegó desde el anuncio: " + strings.TrimSpace(m.Referral.Headline+" "+m.Referral.Body) + "]")
				}
				msgs = append(msgs, im)
			}
			for _, s := range v.Statuses {
				su := StatusUpdate{ID: s.ID, Status: s.Status}
				if len(s.Errors) > 0 {
					su.Error = s.Errors[0].Title
				}
				stats = append(stats, su)
			}
		}
	}
	return msgs, stats, nil
}
