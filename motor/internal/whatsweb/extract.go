package whatsweb

import (
	"fmt"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// content is what the Motor needs from one WhatsApp message.
type content struct {
	Kind  string // text, image, audio, video, document, sticker, location
	Text  string
	Mime  string
	Media whatsmeow.DownloadableMessage // nil when there is nothing worth downloading
	Size  uint64
}

// extract turns a WhatsApp message into the simple shape the engine uses.
// ok is false for things that are not customer messages (reactions, protocol
// notices, key distribution...).
func extract(msg *waE2E.Message) (c content, ok bool) {
	if msg == nil {
		return c, false
	}
	switch {
	case msg.GetConversation() != "":
		return content{Kind: "text", Text: msg.GetConversation()}, true

	case msg.GetExtendedTextMessage() != nil:
		e := msg.GetExtendedTextMessage()
		return content{Kind: "text", Text: withAd(e.GetText(), e.GetContextInfo())}, true

	case msg.GetImageMessage() != nil:
		i := msg.GetImageMessage()
		return content{Kind: "image", Text: withAd(i.GetCaption(), i.GetContextInfo()), Mime: i.GetMimetype(), Media: i, Size: i.GetFileLength()}, true

	case msg.GetVideoMessage() != nil:
		v := msg.GetVideoMessage()
		return content{Kind: "video", Text: withAd(v.GetCaption(), v.GetContextInfo()), Mime: v.GetMimetype()}, true

	case msg.GetAudioMessage() != nil:
		a := msg.GetAudioMessage()
		return content{Kind: "audio", Mime: a.GetMimetype(), Media: a, Size: a.GetFileLength()}, true

	case msg.GetDocumentMessage() != nil:
		d := msg.GetDocumentMessage()
		return content{Kind: "document", Text: strings.TrimSpace(d.GetCaption() + " " + d.GetFileName()), Mime: d.GetMimetype(), Media: d, Size: d.GetFileLength()}, true

	case msg.GetStickerMessage() != nil:
		return content{Kind: "sticker", Mime: msg.GetStickerMessage().GetMimetype()}, true

	case msg.GetLocationMessage() != nil:
		l := msg.GetLocationMessage()
		return content{Kind: "location", Text: strings.TrimSpace(fmt.Sprintf("[Ubicación] %s %s (%.6f, %.6f)", l.GetName(), l.GetAddress(), l.GetDegreesLatitude(), l.GetDegreesLongitude()))}, true

	case msg.GetContactMessage() != nil:
		return content{Kind: "text", Text: "[Contacto compartido] " + msg.GetContactMessage().GetDisplayName()}, true

	case msg.GetButtonsResponseMessage() != nil:
		return content{Kind: "text", Text: msg.GetButtonsResponseMessage().GetSelectedDisplayText()}, true

	case msg.GetListResponseMessage() != nil:
		return content{Kind: "text", Text: msg.GetListResponseMessage().GetTitle()}, true
	}
	return c, false
}

// withAd appends where a click-to-WhatsApp ad conversation came from, the same
// note the Cloud API channel adds, so the seller knows which product it is.
func withAd(text string, ci *waE2E.ContextInfo) string {
	ad := ci.GetExternalAdReply()
	if ad == nil {
		return text
	}
	src := strings.TrimSpace(ad.GetTitle() + " " + ad.GetBody())
	if src == "" {
		return text
	}
	return strings.TrimSpace(text + "\n[Llegó desde el anuncio: " + src + "]")
}

var nonDigit = regexp.MustCompile(`\D+`)

// ParseTo turns the phone numbers used across the Motor ("573001234567",
// "3001234567", "+57 300 123 4567" or "123@lid") into a WhatsApp address.
func ParseTo(to string) (types.JID, error) {
	to = strings.TrimSpace(to)
	if user, ok := strings.CutSuffix(to, "@lid"); ok && user != "" {
		return types.NewJID(user, types.HiddenUserServer), nil
	}
	if strings.Contains(to, "@") {
		return types.ParseJID(to)
	}
	d := nonDigit.ReplaceAllString(to, "")
	// A Colombian mobile written without the country code.
	if len(d) == 10 && strings.HasPrefix(d, "3") {
		d = "57" + d
	}
	if len(d) < 8 {
		return types.EmptyJID, fmt.Errorf("el número %q no es válido", to)
	}
	return types.NewJID(d, types.DefaultUserServer), nil
}

// isCustomerChat reports whether the chat is a one-to-one conversation (not a
// group, a broadcast list, a status or a channel).
func isCustomerChat(j types.JID) bool {
	return j.Server == types.DefaultUserServer || j.Server == types.HiddenUserServer
}

// phoneKey is the identifier the Motor uses for a contact: the phone number in
// international format without symbols, as the Cloud API channel does. When
// WhatsApp only reveals an anonymous id (LID) and the number cannot be
// resolved, "<id>@lid" is used so the conversation still works.
func phoneKey(chat, alt, resolved types.JID) string {
	chat = chat.ToNonAD()
	switch {
	case chat.Server == types.DefaultUserServer:
		return chat.User
	case !resolved.IsEmpty() && resolved.Server == types.DefaultUserServer:
		return resolved.User
	case !alt.IsEmpty() && alt.Server == types.DefaultUserServer:
		return alt.User
	case chat.Server == types.HiddenUserServer:
		return chat.User + "@lid"
	}
	return ""
}
