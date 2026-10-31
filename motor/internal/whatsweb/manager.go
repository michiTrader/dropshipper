// Package whatsweb connects the Motor to WhatsApp as a "linked device" (the
// same mechanism as WhatsApp Web): the owner scans a QR code once and the AI can
// then read and write messages without the official Cloud API.
//
// It is an unofficial channel. WhatsApp can restrict numbers that automate
// messages, so it should be used with a dedicated number. The Cloud API channel
// (internal/whatsapp) stays the recommended option once the business is verified.
package whatsweb

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // database/sql driver used for the session store

	"github.com/michiTrader/dropshipper/motor/internal/whatsapp"
)

// Connection states shown in the panel.
const (
	StateOff        = "apagado"        // the channel is not selected
	StateUnlinked   = "sin_vincular"   // selected, but no phone linked yet
	StateWaitingQR  = "esperando_qr"   // a QR code is on screen
	StateLinking    = "vinculando"     // the phone scanned the code
	StateConnecting = "conectando"     // opening the session
	StateConnected  = "conectado"      // ready to send and receive
	StateReconnect  = "reconectando"   // lost the connection, retrying
	StateLoggedOut  = "sesion_cerrada" // the phone removed the linked device
)

// ErrNotConnected is returned when sending without a live session.
var ErrNotConnected = errors.New("WhatsApp Web no está conectado: abre Conexiones y vincula el teléfono con el código QR")

const (
	maxMediaBytes = 25 << 20
	maxAge        = 6 * time.Hour // older messages are not answered after a long outage
	recentKeep    = 500
)

// Message is one customer message received over WhatsApp Web.
type Message struct {
	Phone     string
	Name      string
	Kind      string // text, image, audio, video, document, sticker, location
	Text      string
	MediaPath string // file name inside the media directory
	WAID      string
	At        time.Time
}

// Info is the connection status for the panel.
type Info struct {
	Estado    string `json:"estado"`
	Vinculado bool   `json:"vinculado"`
	Numero    string `json:"numero,omitempty"`
	Nombre    string `json:"nombre,omitempty"`
	QR        string `json:"qr,omitempty"` // PNG as a data URL
	Error     string `json:"error,omitempty"`
}

type recentMsg struct {
	chat, sender types.JID
	at           time.Time
}

// Manager owns the WhatsApp Web session. All methods are safe on a nil receiver
// (the command-line tool runs without a channel).
type Manager struct {
	dbPath   string
	mediaDir string
	log      *log.Logger

	mu        sync.Mutex
	onMessage func(ctx context.Context, m Message)
	onReceipt func(waIDs []string, status string) // status: delivered | read
	onChange  func()                              // the connection status changed
	enabled   bool
	container *sqlstore.Container
	cli       *whatsmeow.Client
	state     string
	qr        string
	lastErr   string
	ignore    map[string]bool
	recent    map[string]recentMsg
	order     []string

	inbox chan *events.Message
	done  chan struct{}
	once  sync.Once
}

// New creates a manager that keeps the session in dir/whatsapp_web.db and saves
// received files (payment receipts) into mediaDir.
func New(dir, mediaDir string, lg *log.Logger) *Manager {
	if lg == nil {
		lg = log.New(os.Stderr, "", log.LstdFlags)
	}
	// What the owner sees under "Linked devices" on the phone.
	store.SetOSInfo("Dropshipper Motor", [3]uint32{0, 1, 0})
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()

	m := &Manager{
		dbPath: filepath.Join(dir, "whatsapp_web.db"), mediaDir: mediaDir, log: lg,
		state: StateOff, ignore: map[string]bool{}, recent: map[string]recentMsg{},
		inbox: make(chan *events.Message, 256), done: make(chan struct{}),
	}
	go m.worker()
	return m
}

// SetHandlers registers what happens with incoming messages, delivery receipts
// and status changes. Any of them may be nil.
func (m *Manager) SetHandlers(onMessage func(context.Context, Message), onReceipt func([]string, string), onChange func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onMessage, m.onReceipt, m.onChange = onMessage, onReceipt, onChange
	m.mu.Unlock()
}

// Configure turns the channel on or off and sets the numbers to ignore. It is
// idempotent: the hub calls it on every settings change.
func (m *Manager) Configure(on bool, ignore []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ignore = map[string]bool{}
	for _, n := range ignore {
		if k := digits(n); k != "" {
			m.ignore[k] = true
		}
	}
	was := m.enabled
	m.enabled = on
	m.mu.Unlock()
	switch {
	case on && !was:
		go m.autoConnect()
	case !on && was:
		m.mu.Lock()
		m.stopLocked()
		m.state, m.qr, m.lastErr = StateOff, "", ""
		m.mu.Unlock()
		m.changed()
	}
}

// Close disconnects and releases the session store.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.once.Do(func() { close(m.done) })
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	if m.container != nil {
		_ = m.container.Close()
		m.container = nil
	}
}

// Enabled reports whether the channel is selected.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

// Available reports whether messages can be sent right now.
func (m *Manager) Available() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled && m.cli != nil && m.cli.Store.ID != nil
}

// Info returns the current status (with the QR code while one is on screen).
func (m *Manager) Info() Info {
	if m == nil {
		return Info{Estado: StateOff}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	in := Info{Estado: m.state, Error: m.lastErr}
	if m.cli != nil && m.cli.Store.ID != nil {
		in.Vinculado = true
		in.Numero = m.cli.Store.ID.User
		in.Nombre = m.cli.Store.PushName
	}
	if m.state == StateWaitingQR && m.qr != "" {
		if png, err := qrcode.Encode(m.qr, qrcode.Medium, 288); err == nil {
			in.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	return in
}

// ---------- lifecycle ----------

func (m *Manager) loggerFor() waLog.Logger { return &logAdapter{lg: m.log} }

func (m *Manager) ensureLocked(ctx context.Context) error {
	if m.container == nil {
		if err := os.MkdirAll(filepath.Dir(m.dbPath), 0o755); err != nil {
			return err
		}
		dsn := "file:" + m.dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
		c, err := sqlstore.New(ctx, "sqlite", dsn, m.loggerFor())
		if err != nil {
			return fmt.Errorf("abrir la sesión de WhatsApp Web: %w", err)
		}
		m.container = c
	}
	if m.cli == nil {
		dev, err := m.container.GetFirstDevice(ctx)
		if err != nil {
			return fmt.Errorf("leer la sesión de WhatsApp Web: %w", err)
		}
		cli := whatsmeow.NewClient(dev, m.loggerFor())
		cli.EnableAutoReconnect = true
		cli.InitialAutoReconnect = true
		cli.AddEventHandler(func(evt any) { m.onEvent(cli, evt) })
		m.cli = cli
	}
	return nil
}

func (m *Manager) stopLocked() {
	if m.cli != nil {
		m.cli.Disconnect()
	}
}

func (m *Manager) changed() {
	if m == nil {
		return
	}
	m.mu.Lock()
	fn := m.onChange
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// autoConnect resumes a saved session when the Motor starts or the channel is
// selected. Without a session it waits for the owner to press "Vincular".
func (m *Manager) autoConnect() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m.mu.Lock()
	if !m.enabled {
		m.mu.Unlock()
		return
	}
	if err := m.ensureLocked(ctx); err != nil {
		m.lastErr, m.state = err.Error(), StateUnlinked
		m.mu.Unlock()
		m.log.Printf("WhatsApp Web: %v", err)
		m.changed()
		return
	}
	cli := m.cli
	if cli.Store.ID == nil {
		m.state, m.lastErr = StateUnlinked, ""
		m.mu.Unlock()
		m.changed()
		return
	}
	m.state, m.lastErr = StateConnecting, ""
	m.mu.Unlock()
	m.changed()
	m.connect(cli)
}

func (m *Manager) connect(cli *whatsmeow.Client) {
	if err := cli.Connect(); err != nil {
		m.mu.Lock()
		if m.cli == cli {
			m.state, m.lastErr = StateReconnect, "No se pudo conectar con WhatsApp: "+err.Error()
		}
		m.mu.Unlock()
		m.log.Printf("WhatsApp Web: conectar: %v", err)
		m.changed()
	}
}

// StartPairing begins linking a phone: it shows a QR code that the owner scans
// from WhatsApp > Linked devices. With a saved session it just reconnects.
func (m *Manager) StartPairing(ctx context.Context) error {
	if m == nil {
		return errors.New("WhatsApp Web no está disponible en este modo")
	}
	m.mu.Lock()
	if !m.enabled {
		m.mu.Unlock()
		return errors.New("primero elige el canal WhatsApp Web y guarda")
	}
	if err := m.ensureLocked(ctx); err != nil {
		m.mu.Unlock()
		return err
	}
	cli := m.cli
	if cli.Store.ID != nil {
		if m.state != StateConnected && m.state != StateConnecting {
			m.state, m.lastErr = StateConnecting, ""
			go m.connect(cli)
		}
		m.mu.Unlock()
		m.changed()
		return nil
	}
	if m.state == StateWaitingQR || m.state == StateLinking {
		m.mu.Unlock()
		return nil
	}
	qrCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	ch, err := cli.GetQRChannel(qrCtx)
	if err != nil {
		cancel()
		m.mu.Unlock()
		if errors.Is(err, whatsmeow.ErrQRAlreadyConnected) {
			return nil
		}
		return err
	}
	m.state, m.qr, m.lastErr = StateWaitingQR, "", ""
	m.mu.Unlock()
	m.changed()
	go func() {
		defer cancel()
		m.consumeQR(cli, ch)
	}()
	go m.connect(cli)
	return nil
}

func (m *Manager) consumeQR(cli *whatsmeow.Client, ch <-chan whatsmeow.QRChannelItem) {
	for item := range ch {
		m.mu.Lock()
		if m.cli != cli {
			m.mu.Unlock()
			return
		}
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			m.state, m.qr = StateWaitingQR, item.Code
		case "success":
			m.state, m.qr, m.lastErr = StateConnecting, "", ""
		case "timeout":
			m.state, m.qr = StateUnlinked, ""
			m.lastErr = "El código QR caducó. Pulsa Vincular para generar uno nuevo."
			go cli.Disconnect()
		case "err-scanned-without-multidevice":
			m.state, m.qr = StateUnlinked, ""
			m.lastErr = "Ese WhatsApp no admite dispositivos vinculados. Actualiza la aplicación en el teléfono."
			go cli.Disconnect()
		case whatsmeow.QRChannelEventError:
			m.state, m.qr = StateUnlinked, ""
			m.lastErr = "No se pudo vincular: " + errText(item.Error)
			go cli.Disconnect()
		default:
			m.state, m.qr = StateUnlinked, ""
			m.lastErr = "No se pudo vincular (" + item.Event + "). Inténtalo de nuevo."
			go cli.Disconnect()
		}
		m.mu.Unlock()
		m.changed()
	}
}

// PairCode links the phone with an 8-character code instead of a QR, for when
// the panel is open on the same phone that holds the WhatsApp account.
func (m *Manager) PairCode(ctx context.Context, phone string) (string, error) {
	if m == nil {
		return "", errors.New("WhatsApp Web no está disponible en este modo")
	}
	if err := m.StartPairing(ctx); err != nil {
		return "", err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		m.mu.Lock()
		cli, st := m.cli, m.state
		m.mu.Unlock()
		if st == StateWaitingQR && cli != nil && cli.IsConnected() {
			code, err := cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
			if err != nil {
				return "", fmt.Errorf("no se pudo pedir el código: %w", err)
			}
			return code, nil
		}
		if (st != StateWaitingQR && st != StateUnlinked) || time.Now().After(deadline) {
			return "", errors.New("no se pudo abrir la conexión con WhatsApp; revisa tu internet e inténtalo de nuevo")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// Unlink removes the linked device from the phone and forgets the session.
func (m *Manager) Unlink(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cli := m.cli
	if cli == nil {
		if m.container == nil && m.enabled {
			if err := m.ensureLocked(ctx); err != nil {
				return err
			}
			cli = m.cli
		}
		if cli == nil {
			return nil
		}
	}
	var warn error
	if cli.Store.ID != nil {
		if err := cli.Logout(ctx); err != nil {
			// The phone may be offline: forget the session here anyway.
			warn = err
			cli.Disconnect()
			if err := cli.Store.Delete(ctx); err != nil {
				return err
			}
		}
	} else {
		cli.Disconnect()
	}
	m.cli = nil
	m.qr, m.lastErr = "", ""
	if m.enabled {
		m.state = StateUnlinked
	} else {
		m.state = StateOff
	}
	if warn != nil {
		m.log.Printf("WhatsApp Web: desvincular (el teléfono no respondió, se olvidó la sesión aquí): %v", warn)
	}
	go m.changed()
	return nil
}

// ---------- events ----------

func (m *Manager) onEvent(cli *whatsmeow.Client, evt any) {
	switch v := evt.(type) {
	case *events.Message:
		select {
		case m.inbox <- v:
		default:
			m.log.Printf("WhatsApp Web: demasiados mensajes en cola, se descarta uno")
		}
		return
	case *events.Receipt:
		m.receipt(v)
		return
	}

	m.mu.Lock()
	if m.cli != cli {
		m.mu.Unlock()
		return
	}
	switch v := evt.(type) {
	case *events.PairSuccess:
		m.state, m.qr, m.lastErr = StateLinking, "", ""
	case *events.Connected:
		m.state, m.qr, m.lastErr = StateConnected, "", ""
	case *events.Disconnected:
		if m.state == StateConnected || m.state == StateConnecting {
			m.state = StateReconnect
		}
	case *events.LoggedOut:
		m.cli = nil
		m.state, m.qr = StateLoggedOut, ""
		m.lastErr = "WhatsApp cerró la sesión (se quitó el dispositivo desde el teléfono). Vuelve a vincularlo."
		go cli.Disconnect()
	case *events.StreamReplaced:
		m.state = StateReconnect
		m.lastErr = "Esta cuenta se abrió en otro lugar con la misma sesión. Revisa que no haya otro Motor conectado al mismo número."
	case *events.TemporaryBan:
		m.lastErr = "WhatsApp suspendió temporalmente el número: " + v.String()
	case *events.ConnectFailure:
		m.lastErr = "WhatsApp rechazó la conexión: " + v.Reason.String()
	case *events.ClientOutdated:
		m.state = StateReconnect
		m.lastErr = "WhatsApp exige una versión más nueva de la librería. Actualiza el Motor."
	default:
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.changed()
}

func (m *Manager) receipt(v *events.Receipt) {
	m.mu.Lock()
	fn := m.onReceipt
	m.mu.Unlock()
	if fn == nil || v.IsFromMe || len(v.MessageIDs) == 0 {
		return
	}
	var status string
	switch v.Type {
	case types.ReceiptTypeDelivered:
		status = "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypePlayed:
		status = "read"
	default:
		return
	}
	fn(v.MessageIDs, status)
}

// worker handles incoming messages one at a time, in order.
func (m *Manager) worker() {
	for {
		select {
		case <-m.done:
			return
		case evt := <-m.inbox:
			func() {
				defer func() {
					if r := recover(); r != nil {
						m.log.Printf("WhatsApp Web: error al procesar un mensaje: %v", r)
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				m.handle(ctx, evt)
			}()
		}
	}
}

func (m *Manager) handle(ctx context.Context, evt *events.Message) {
	info := evt.Info
	if info.IsFromMe || info.IsGroup || !isCustomerChat(info.Chat) || evt.IsEdit {
		return
	}
	if !info.Timestamp.IsZero() && time.Since(info.Timestamp) > maxAge {
		return
	}
	c, ok := extract(evt.Message)
	if !ok {
		return
	}
	m.mu.Lock()
	cli, deliver := m.cli, m.onMessage
	m.mu.Unlock()
	if cli == nil || deliver == nil {
		return
	}
	var resolved types.JID
	if info.Chat.Server == types.HiddenUserServer && cli.Store.LIDs != nil {
		if pn, err := cli.Store.LIDs.GetPNForLID(ctx, info.Chat.ToNonAD()); err == nil {
			resolved = pn
		}
	}
	phone := phoneKey(info.Chat, info.SenderAlt, resolved)
	if phone == "" {
		return
	}
	m.mu.Lock()
	skip := m.ignore[digits(phone)]
	if !skip {
		m.rememberLocked(info.ID, recentMsg{chat: info.Chat, sender: info.Sender, at: info.Timestamp})
	}
	m.mu.Unlock()
	if skip {
		return
	}

	msg := Message{Phone: phone, Name: info.PushName, Kind: c.Kind, Text: c.Text, WAID: info.ID, At: info.Timestamp}
	if c.Media != nil && c.Size <= maxMediaBytes && (c.Kind == "image" || c.Kind == "document" || c.Kind == "audio") {
		if name, err := m.saveMedia(ctx, cli, c, info.ID); err != nil {
			m.log.Printf("WhatsApp Web: descargar archivo de %s: %v", phone, err)
		} else {
			msg.MediaPath = name
		}
	}
	deliver(ctx, msg)
}

func (m *Manager) saveMedia(ctx context.Context, cli *whatsmeow.Client, c content, id string) (string, error) {
	data, err := cli.Download(ctx, c.Media)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(m.mediaDir, 0o755); err != nil {
		return "", err
	}
	name := whatsapp.SafeName(id) + whatsapp.ExtFor(c.Mime)
	if err := os.WriteFile(filepath.Join(m.mediaDir, name), data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func (m *Manager) rememberLocked(id string, r recentMsg) {
	if id == "" {
		return
	}
	if _, ok := m.recent[id]; !ok {
		m.order = append(m.order, id)
		if len(m.order) > recentKeep {
			delete(m.recent, m.order[0])
			m.order = m.order[1:]
		}
	}
	m.recent[id] = r
}

// ---------- sending ----------

// waitConnected gives a dropped connection a few seconds to come back.
func (m *Manager) waitConnected(ctx context.Context) (*whatsmeow.Client, error) {
	deadline := time.Now().Add(8 * time.Second)
	for {
		m.mu.Lock()
		cli, on := m.cli, m.enabled
		m.mu.Unlock()
		if !on || cli == nil || cli.Store.ID == nil {
			return nil, ErrNotConnected
		}
		if cli.IsConnected() {
			return cli, nil
		}
		if time.Now().After(deadline) {
			return nil, ErrNotConnected
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// SendText sends a text message to a phone number.
func (m *Manager) SendText(ctx context.Context, to, text string) (string, error) {
	if m == nil {
		return "", ErrNotConnected
	}
	jid, err := ParseTo(to)
	if err != nil {
		return "", err
	}
	cli, err := m.waitConnected(ctx)
	if err != nil {
		return "", err
	}
	resp, err := cli.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return "", fmt.Errorf("WhatsApp Web no pudo enviar el mensaje: %w", err)
	}
	return resp.ID, nil
}

// MarkRead marks the customer's message as read and, when typing is true, shows
// "escribiendo..." while the AI prepares its answer.
func (m *Manager) MarkRead(ctx context.Context, waID string, typing bool) error {
	if m == nil {
		return ErrNotConnected
	}
	m.mu.Lock()
	r, ok := m.recent[waID]
	cli := m.cli
	m.mu.Unlock()
	if !ok || cli == nil || !cli.IsConnected() {
		return nil
	}
	if err := cli.MarkRead(ctx, []types.MessageID{waID}, time.Now(), r.chat, r.sender); err != nil {
		return err
	}
	if typing {
		return cli.SendChatPresence(ctx, r.chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	}
	return nil
}

// ---------- helpers ----------

func digits(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), "@lid")
	d := nonDigit.ReplaceAllString(s, "")
	if len(d) == 10 && strings.HasPrefix(d, "3") {
		d = "57" + d
	}
	return d
}

func errText(err error) string {
	if err == nil {
		return "error desconocido"
	}
	return err.Error()
}

// logAdapter sends the library's warnings and errors to the Motor's log.
type logAdapter struct {
	lg  *log.Logger
	mod string
}

func (l *logAdapter) Warnf(msg string, a ...any)  { l.out("aviso", msg, a) }
func (l *logAdapter) Errorf(msg string, a ...any) { l.out("error", msg, a) }
func (l *logAdapter) Infof(string, ...any)        {}
func (l *logAdapter) Debugf(string, ...any)       {}
func (l *logAdapter) Sub(mod string) waLog.Logger { return &logAdapter{lg: l.lg, mod: mod} }
func (l *logAdapter) out(level, msg string, a []any) {
	prefix := "WhatsApp Web"
	if l.mod != "" {
		prefix += "/" + l.mod
	}
	l.lg.Printf("%s %s: %s", prefix, level, fmt.Sprintf(msg, a...))
}
