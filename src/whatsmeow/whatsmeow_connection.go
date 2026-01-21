package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"

	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"

	library "github.com/nocodeleaks/quepasa/library"
	whatsapp "github.com/nocodeleaks/quepasa/whatsapp"
	whatsmeow "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	types "go.mau.fi/whatsmeow/types"
)

// Must Implement IWhatsappConnection
type WhatsmeowConnection struct {
	library.LogStruct // logging
	Client            *whatsmeow.Client

	Handlers        *WhatsmeowHandlers       // composition for handlers
	GroupManager    *WhatsmeowGroupManager   // composition for group operations
	StatusManager   *WhatsmeowStatusManager  // composition for status operations
	ContactManager  *WhatsmeowContactManager // composition for contact operations
	WakeUpScheduler *WakeUpScheduler         // composition for scheduled presence wake-ups
	// call managers intentionally omitted per request (do not include CallManager / SIPCallManager)

	failedToken  bool
	paired       func(string)
	IsConnecting bool `json:"isconnecting"` // used to avoid multiple connection attempts
}

//#region IMPLEMENT WHATSAPP CONNECTION OPTIONS INTERFACE

// get default log entry, never nil
func (source *WhatsmeowConnection) GetLogger() *log.Entry {
	if source != nil && source.LogEntry != nil {
		return source.LogEntry
	}

	logentry := library.NewLogEntry(source)
	if source != nil {
		statusManager := source.GetStatusManager()
		wid, _ := statusManager.GetWidInternal()
		if len(wid) > 0 {
			logentry = logentry.WithField(LogFields.WId, wid)
		}
		source.LogEntry = logentry
	}

	logentry.Level = log.ErrorLevel
	logentry.Infof("generating new log entry for %s, with level: %s", reflect.TypeOf(source), logentry.Level)
	return logentry
}

//#endregion

//region IMPLEMENT INTERFACE WHATSAPP CONNECTION

// returns a valid chat title from local memory store
func (conn *WhatsmeowConnection) GetChatTitle(wid string) string {
	return GetChatTitleFromWId(conn, wid)
}

// Connect to websocket only, dot not authenticate yet, errors come after
func (source *WhatsmeowConnection) Connect() (err error) {
	source.GetLogger().Info("starting whatsmeow connection")

	if source.IsConnecting {
		return
	}

	source.IsConnecting = true

	err = source.Client.Connect()
	source.IsConnecting = false

	if err != nil {
		source.failedToken = true
		return
	}

	// waits 2 seconds for loggedin
	// not required
	_ = source.Client.WaitForConnection(time.Millisecond * 2000)

	source.failedToken = false
	return
}

// func (cli *Client) Download(msg DownloadableMessage) (data []byte, err error)
func (source *WhatsmeowConnection) DownloadData(imsg whatsapp.IWhatsappMessage) (data []byte, err error) {
	msg := imsg.GetSource()
	logentry := source.GetLogger().WithField(LogFields.MessageId, imsg.GetId())

	// Try direct downloadable message first
	downloadable, ok := msg.(whatsmeow.DownloadableMessage)
	if ok {
		logentry.Trace("Message implements DownloadableMessage directly, using Client.Download()")
		return source.Client.Download(context.Background(), downloadable)
	}

	waMsg, ok := msg.(*waE2E.Message)
	if ok {
		downloadable = GetDownloadableMessage(waMsg)
		if downloadable != nil {
			logentry.Trace("waMsg implements DownloadableMessage, using Client.Download()")
			return source.Client.Download(context.Background(), downloadable)
		}
	}

	// If internal content as VCard or Localization
	attach := imsg.GetAttachment()
	if attach != nil {
		data := attach.GetContent()
		if data != nil {
			logentry.Trace("no waMsg, found attachment, returning content")
			return *data, err
		}
	}
	// If we reach here, it means we have a waE2E.Message but no DownloadableMessage interface
	return nil, fmt.Errorf("message (%s) is not downloadable", imsg.GetId())
}

func (conn *WhatsmeowConnection) Download(imsg whatsapp.IWhatsappMessage, cache bool) (att *whatsapp.WhatsappAttachment, err error) {
	logentry := conn.GetLogger().WithField(LogFields.MessageId, imsg.GetId())
	logentry.Tracef("Download() method called, Cache: %v", cache)

	att = imsg.GetAttachment()
	if att == nil {
		return nil, fmt.Errorf("message (%s) does not contains attachment info", imsg.GetId())
	}

	if cache && att.HasContent() {
		logentry.Debugf("Download() using cached content - HasContent: %v", att.HasContent())
		return att, nil
	}

	data, err := conn.DownloadData(imsg)
	if err != nil {
		return nil, fmt.Errorf("failed to download data for message (%s): %v", imsg.GetId(), err)
	}

	if !cache {
		newAtt := *att
		att = &newAtt
	}

	att.SetContent(&data)
	return
}

func (source *WhatsmeowConnection) Revoke(msg whatsapp.IWhatsappMessage) error {
	logentry := source.GetLogger()

	jid, err := types.ParseJID(msg.GetChatId())
	if err != nil {
		logentry.Infof("revoke error on get jid: %s", err)
		return err
	}

	participantJid, err := types.ParseJID(msg.GetParticipantId())
	if err != nil {
		logentry.Infof("revoke error on get jid: %s", err)
		return err
	}

	newMessage := source.Client.BuildRevoke(jid, participantJid, msg.GetId())
	_, err = source.Client.SendMessage(context.Background(), jid, newMessage)
	if err != nil {
		logentry.Infof("revoke error: %s", err)
		return err
	}

	return nil
}

// Edit edits an existing message with new content
func (source *WhatsmeowConnection) Edit(msg whatsapp.IWhatsappMessage, newContent string) error {
	logentry := source.GetLogger()

	jid, err := types.ParseJID(msg.GetChatId())
	if err != nil {
		err := fmt.Errorf("failed to edit message (%s): %w", msg.GetId(), err)
		logentry.Error(err)
		return err
	}

	// Build text message with new content
	textMessage := &waE2E.Message{
		Conversation: &newContent,
	}

	// Build edit message
	editMessage := source.Client.BuildEdit(jid, msg.GetId(), textMessage)
	_, err = source.Client.SendMessage(context.Background(), jid, editMessage)
	if err != nil {
		err := fmt.Errorf("failed to edit message (%s): %w", msg.GetId(), err)
		logentry.Error(err)
		return err
	}

	return nil
}

// MarkRead sends a read receipt for the given message via Whatsmeow handlers
func (source *WhatsmeowConnection) MarkRead(imsg whatsapp.IWhatsappMessage) error {
	if imsg == nil {
		return fmt.Errorf("nil message")
	}
	id := imsg.GetId()
	logentry := source.GetLogger().WithField(LogFields.MessageId, id)

	msg, ok := imsg.(*whatsapp.WhatsappMessage)
	if !ok {
		msg = &whatsapp.WhatsappMessage{
			Id:        imsg.GetId(),
			Timestamp: time.Now(),
			Chat:      whatsapp.WhatsappChat{Id: imsg.GetChatId()},
		}
	}

	// default to ReceiptTypeRead
	err := source.GetHandlers().MarkRead(msg, types.ReceiptTypeRead)
	if err != nil {
		logentry.Errorf("error marking read: %v", err)
	}
	return err
}

func isASCII(s string) bool {
	for _, c := range s {
		if c > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func (source *WhatsmeowConnection) GetContextInfo(msg whatsapp.WhatsappMessage) *waE2E.ContextInfo {

	var contextInfo *waE2E.ContextInfo
	if len(msg.InReply) > 0 {
		contextInfo = source.GetInReplyContextInfo(msg)
	}

	// mentions ---------------------------------------
	if msg.FromGroup() {
		messageText := msg.GetText()
		mentions := GetMentions(messageText)
		if len(mentions) > 0 {

			if contextInfo == nil {
				contextInfo = &waE2E.ContextInfo{}
			}

			contextInfo.MentionedJID = mentions
		}
	}

	// disapering messages, not implemented yet
	if contextInfo != nil {
		contextInfo.Expiration = proto.Uint32(0)
		contextInfo.EphemeralSettingTimestamp = proto.Int64(0)
		contextInfo.DisappearingMode = &waE2E.DisappearingMode{Initiator: waE2E.DisappearingMode_CHANGED_IN_CHAT.Enum()}
	}

	return contextInfo
}

func (source *WhatsmeowConnection) GetInReplyContextInfo(msg whatsapp.WhatsappMessage) *waE2E.ContextInfo {
	logentry := source.GetLogger()

	// default information for cached messages
	var info types.MessageInfo

	// getting quoted message if available on cache
	// (optional) another devices will process anyway, but our devices will show quoted only if it exists on cache
	var quoted *waE2E.Message

	cached, _ := source.GetHandlers().WAHandlers.GetById(msg.InReply)
	if cached != nil {

		// update cached info
		info, _ = cached.InfoForHistory.(types.MessageInfo)

		if cached.Content != nil {
			if content, ok := cached.Content.(*waE2E.Message); ok {

				// update quoted message content
				quoted = content

			} else {
				logentry.Warnf("content has an invalid type (%s), on reply to msg id: %s", reflect.TypeOf(cached.Content), msg.InReply)
			}
		} else {
			logentry.Warnf("message content not cached, on reply to msg id: %s", msg.InReply)
		}
	} else {
		logentry.Warnf("message not cached, on reply to msg id: %s", msg.InReply)
	}

	var participant *string
	if info.ID != "" {
		var sender string
		if msg.FromGroup() {
			sender = fmt.Sprint(info.Sender.User, "@", info.Sender.Server)
		} else {
			sender = fmt.Sprint(info.Chat.User, "@", info.Chat.Server)
		}
		participant = proto.String(sender)
	}

	return &waE2E.ContextInfo{
		StanzaID:      proto.String(msg.InReply),
		Participant:   participant,
		QuotedMessage: quoted,
	}
}

// Default SEND method using WhatsappMessage Interface
func (source *WhatsmeowConnection) Send(msg *whatsapp.WhatsappMessage) (whatsapp.IWhatsappSendResponse, error) {
	logentry := source.GetLogger()
	loglevel := logentry.Level
	logentry = logentry.WithField(LogFields.MessageId, msg.Id)
	logentry.Level = loglevel

	var err error

	// Formatting destination accordingly
	formattedDestination, _ := whatsapp.FormatEndpoint(msg.GetChatId())

	// avoid common issue with incorrect non ascii chat id
	if !isASCII(formattedDestination) {
		err = fmt.Errorf("not an ASCII formatted chat id")
		return msg, err
	}

	// validating jid before remote commands as upload or send
	jid, err := types.ParseJID(formattedDestination)
	if err != nil {
		return msg, err
	}

	// request message text
	messageText := msg.GetText()

	var newMessage *waE2E.Message

	// MODIFIED: Added List Message support
	if msg.List != nil {
		sections := make([]*waE2E.ListMessage_Section, len(msg.List.Sections))
		for i, s := range msg.List.Sections {
			rows := make([]*waE2E.ListMessage_Row, len(s.Rows))
			for j, r := range s.Rows {
				rows[j] = &waE2E.ListMessage_Row{
					RowID:       proto.String(r.RowId),
					Title:       proto.String(r.Title),
					Description: proto.String(r.Description),
				}
			}
			sections[i] = &waE2E.ListMessage_Section{
				Title: proto.String(s.Title),
				Rows:  rows,
			}
		}

		newMessage = &waE2E.Message{
			ListMessage: &waE2E.ListMessage{
				Title:       proto.String(msg.List.Title),
				Description: proto.String(msg.List.Description),
				ButtonText:  proto.String(msg.List.ButtonText),
				FooterText:  proto.String(msg.List.FooterText),
				ListType:    waE2E.ListMessage_SINGLE_SELECT.Enum(),
				Sections:    sections,
				ContextInfo: source.GetContextInfo(*msg),
			},
		}
	} else if msg.Type == whatsapp.ContactMessageType && msg.Contact != nil {
		// Check if this is a contact message
		contact := msg.Contact

		// Generate vCard if not provided
		vcard := contact.Vcard
		if len(vcard) == 0 {
			// Generate vCard checking if contact is on WhatsApp
			vcard = source.generateVCardForContact(contact)
		}

		newMessage = &waE2E.Message{
			ContactMessage: &waE2E.ContactMessage{
				DisplayName: proto.String(contact.Name),
				Vcard:       proto.String(vcard),
			},
		}
		// Add context info for replies if needed
		if len(msg.InReply) > 0 {
			newMessage.ContactMessage.ContextInfo = source.GetContextInfo(*msg)
		}
	} else if msg.Type == whatsapp.LocationMessageType && msg.HasAttachment() {
		// Check if this is a location message
		attach := msg.Attachment
		newMessage = &waE2E.Message{
			LocationMessage: &waE2E.LocationMessage{
				DegreesLatitude:  proto.Float64(attach.Latitude),
				DegreesLongitude: proto.Float64(attach.Longitude),
			},
		}
		// Add optional fields if available
		if len(messageText) > 0 {
			newMessage.LocationMessage.Name = proto.String(messageText)
		}
		// Add context info for replies if needed
		if len(msg.InReply) > 0 {
			newMessage.LocationMessage.ContextInfo = source.GetContextInfo(*msg)
		}
	} else if !msg.HasAttachment() {
		// Text messages, buttons, polls
		if IsValidForButtons(messageText) {
			internal := GenerateButtonsMessage(messageText)
			internal.ContextInfo = source.GetContextInfo(*msg)
			newMessage = &waE2E.Message{
				ButtonsMessage: internal,
			}
		} else {
			if msg.Poll != nil {
				newMessage, err = GeneratePollMessage(msg)
				if err != nil {
					return msg, err
				}
			} else {
				internal := &waE2E.ExtendedTextMessage{Text: &messageText}
				internal.ContextInfo = source.GetContextInfo(*msg)
				newMessage = &waE2E.Message{ExtendedTextMessage: internal}
			}
		}
	} else {
		// Other attachment types (images, videos, documents, etc.)
		newMessage, err = source.UploadAttachment(*msg)
		if err != nil {
			return msg, err
		}
	}

	// Generating a new unique MessageID
	if len(msg.Id) == 0 {
		msg.Id = source.Client.GenerateMessageID()
	}

	extra := whatsmeow.SendRequestExtra{
		ID: msg.Id,
	}

	// saving cached content for instance of future reply
	if msg.Content == nil {
		msg.Content = newMessage
	}

	resp, err := source.Client.SendMessage(context.Background(), jid, newMessage, extra)
	if err != nil {
		logentry.Errorf("whatsmeow connection send error: %s", err)
		return msg, err
	}

	// updating timestamp
	msg.Timestamp = resp.Timestamp

	if msg.Id != resp.ID {
		logentry.Warnf("send success but msg id differs from response id: %s, type: %v, on: %s", resp.ID, msg.Type, msg.Timestamp)
	} else {
		logentry.Infof("send success, type: %v, on: %s", msg.Type, msg.Timestamp)
	}

	// testing, mark read function
	if source.GetHandlers().HandleReadUpdate() {
		go source.GetHandlers().MarkRead(msg, types.ReceiptTypeRead)
	}

	return msg, err
}

// useful to check if is a member of a group before send a msg.
// fails on recently added groups.
// pending a more efficient code !!!!!!!!!!!!!!
func (source *WhatsmeowConnection) HasChat(chat string) bool {
	jid, err := types.ParseJID(chat)
	if err != nil {
		return false
	}

	info, err := source.Client.Store.ChatSettings.GetChatSettings(context.TODO(), jid)
	if err != nil {
		return false
	}

	return info.Found
}

// func (cli *Client) Upload(ctx context.Context, plaintext []byte, appInfo MediaType) (resp UploadResponse, err error)
func (source *WhatsmeowConnection) UploadAttachment(msg whatsapp.WhatsappMessage) (result *waE2E.Message, err error) {

	content := *msg.Attachment.GetContent()
	if len(content) == 0 {
		err = fmt.Errorf("null or empty content")
		return
	}

	mediaType := GetMediaTypeFromWAMsgType(msg.Type)
	response, err := source.Client.Upload(context.Background(), content, mediaType)
	if err != nil {
		return
	}

	inreplycontext := source.GetInReplyContextInfo(msg)
	result = NewWhatsmeowMessageAttachment(response, msg, mediaType, inreplycontext)
	return
}

func (conn *WhatsmeowConnection) Disconnect() (err error) {
	if conn.Client != nil {
		if conn.Client.IsConnected() {
			conn.Client.Disconnect()
		}
	}
	return
}

//region PAIRING

// dispatchPairRequestEvent dispatches a system event when pairing is requested
func (conn *WhatsmeowConnection) dispatchPairRequestEvent(phone string) {
	handlers := conn.GetHandlers()
	if handlers == nil || handlers.WAHandlers == nil || handlers.WAHandlers.IsInterfaceNil() {
		return
	}

	logger := conn.GetLogger()
	logger.Debug("dispatching pair_request event")

	// Create pairing request event message with JSON details
	eventData := map[string]interface{}{
		"event":       "pair_request",
		"status":      "requested",
		"message":     "Pairing code requested",
		"phone":       phone,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"description": "User requested pairing code generation for phone number",
	}

	message := &whatsapp.WhatsappMessage{
		Id:        conn.Client.GenerateMessageID(),
		Timestamp: time.Now().UTC(),
		Type:      whatsapp.SystemMessageType,
		FromMe:    false,
		Chat:      whatsapp.WASYSTEMCHAT,
		Text:      library.ToJson(eventData),
		Info:      eventData,
	}

	// Send through dispatcher
	go handlers.WAHandlers.Message(message, "pair_request")
}

// dispatchPairTimeoutEvent dispatches a system event when pairing expires/fails
func (conn *WhatsmeowConnection) dispatchPairTimeoutEvent(phone string, errorMsg string) {
	handlers := conn.GetHandlers()
	if handlers == nil || handlers.WAHandlers == nil || handlers.WAHandlers.IsInterfaceNil() {
		return
	}

	logger := conn.GetLogger()
	logger.Debug("dispatching pair_timeout event")

	// Determine if it's a timeout or other error
	status := "error"
	message := "Pairing code failed"
	description := "Pairing process failed with error"

	if strings.Contains(strings.ToLower(errorMsg), "timeout") || strings.Contains(strings.ToLower(errorMsg), "expired") {
		status = "expired"
		message = "Pairing code expired without being used"
		description = "Pairing code was not used within the allowed time period and has expired"
	}

	// Create pairing timeout event message with JSON details
	eventData := map[string]interface{}{
		"event":       "pair_timeout",
		"status":      status,
		"message":     message,
		"phone":       phone,
		"error":       errorMsg,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"description": description,
	}

	systemMessage := &whatsapp.WhatsappMessage{
		Id:        conn.Client.GenerateMessageID(),
		Timestamp: time.Now().UTC(),
		Type:      whatsapp.SystemMessageType,
		FromMe:    false,
		Chat:      whatsapp.WASYSTEMCHAT,
		Text:      library.ToJson(eventData),
		Info:      eventData,
	}

	// Send through dispatcher
	go handlers.WAHandlers.Message(systemMessage, "pair_timeout")
}

// dispatchQRRequestEvent dispatches a system event when QR code is requested
func (conn *WhatsmeowConnection) dispatchQRRequestEvent() {
	handlers := conn.GetHandlers()
	if handlers == nil || handlers.WAHandlers == nil || handlers.WAHandlers.IsInterfaceNil() {
		return
	}

	logger := conn.GetLogger()
	logger.Debug("dispatching qr_request event")

	// Get phone number from connection
	phone := ""
	if conn.Client != nil && conn.Client.Store != nil {
		jid := conn.Client.Store.ID
		if jid != nil {
			phone = jid.User
		}
	}

	// Create QR request event message with JSON details
	eventData := map[string]interface{}{
		"event":       "qr_request",
		"status":      "requested",
		"message":     "QR code scan requested",
		"phone":       phone,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"description": "User requested QR code generation for WhatsApp pairing",
	}

	message := &whatsapp.WhatsappMessage{
		Id:        conn.Client.GenerateMessageID(),
		Timestamp: time.Now().UTC(),
		Type:      whatsapp.SystemMessageType,
		FromMe:    false,
		Chat:      whatsapp.WASYSTEMCHAT,
		Text:      library.ToJson(eventData),
		Info:      eventData,
	}

	// Send through dispatcher
	go handlers.WAHandlers.Message(message, "qr_request")
}

// dispatchQRTimeoutEvent dispatches a system event when QR code expires/times out
func (conn *WhatsmeowConnection) dispatchQRTimeoutEvent() {
	handlers := conn.GetHandlers()
	if handlers == nil || handlers.WAHandlers == nil || handlers.WAHandlers.IsInterfaceNil() {
		return
	}

	logger := conn.GetLogger()
	logger.Debug("dispatching qr_timeout event")

	// Get phone number from connection
	phone := ""
	if conn.Client != nil && conn.Client.Store != nil {
		jid := conn.Client.Store.ID
		if jid != nil {
			phone = jid.User
		}
	}

	// Create QR timeout event message with JSON details
	eventData := map[string]interface{}{
		"event":       "qr_timeout",
		"status":      "expired",
		"message":     "QR code expired without being scanned",
		"phone":       phone,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"description": "QR code was not scanned within the allowed time period and has expired",
	}

	message := &whatsapp.WhatsappMessage{
		Id:        conn.Client.GenerateMessageID(),
		Timestamp: time.Now().UTC(),
		Type:      whatsapp.SystemMessageType,
		FromMe:    false,
		Chat:      whatsapp.WASYSTEMCHAT,
		Text:      library.ToJson(eventData),
		Info:      eventData,
	}

	// Send through dispatcher
	go handlers.WAHandlers.Message(message, "qr_timeout")
}

func (source *WhatsmeowConnection) PairPhone(phone string) (string, error) {
	logger := source.GetLogger()
	logger.Infof("Pairing requested for phone: %s", phone)

	// Dispatch pair_request event before starting pairing process
	source.dispatchPairRequestEvent(phone)

	if !source.Client.IsConnected() {
		err := source.Client.Connect()
		if err != nil {
			log.Errorf("error on connecting for getting whatsapp qrcode: %s", err.Error())
			return "", err
		}
	}

	code, err := source.Client.PairPhone(context.TODO(), phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")

	// Check if pairing failed due to timeout or error
	if err != nil {
		// Dispatch pair timeout/error event
		source.dispatchPairTimeoutEvent(phone, err.Error())
	}

	return code, err
}

func (conn *WhatsmeowConnection) GetWhatsAppQRCode() string {

	var result string

	logger := conn.GetLogger()
	logger.Info("QR code requested")

	// Dispatch qr_request event before starting QR process
	conn.dispatchQRRequestEvent()

	// No ID stored, new login
	qrChan, err := conn.Client.GetQRChannel(context.Background())
	if err != nil {
		log.Errorf("error on getting whatsapp qrcode channel: %s", err.Error())
		return ""
	}

	if !conn.Client.IsConnected() {
		err = conn.Client.Connect()
		if err != nil {
			log.Errorf("error on connecting for getting whatsapp qrcode: %s", err.Error())
			return ""
		}
	}

	evt, ok := <-qrChan
	if ok {
		switch evt.Event {
		case "code":
			result = evt.Code
		case "timeout":
			// QR code timed out - dispatch event
			logger.Warn("QR code timed out")
			conn.dispatchQRTimeoutEvent()
		}
	}
	return result
}

//endregion

func TryUpdateChannel(ch chan<- string, value string) (closed bool) {
	defer func() {
		if recover() != nil {
			// the return result can be altered
			// in a defer function call
			closed = false
		}
	}()

	ch <- value // panic if ch is closed
	return true // <=> closed = false; return
}

func (source *WhatsmeowConnection) GetWhatsAppQRChannel(ctx context.Context, out chan<- string) error {
	logger := source.GetLogger()

	// No ID stored, new login
	qrChan, err := source.Client.GetQRChannel(ctx)
	if err != nil {
		logger.Errorf("error on getting whatsapp qrcode channel: %s", err.Error())
		return err
	}

	if !source.Client.IsConnected() {
		err = source.Client.Connect()
		if err != nil {
			logger.Errorf("error on connecting for getting whatsapp qrcode: %s", err.Error())
			return err
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)

	for evt := range qrChan {
		if evt.Event == "code" {
			if !TryUpdateChannel(out, evt.Code) {
				// expected error, means that websocket was closed
				// probably user has gone out page
				return fmt.Errorf("cant write to output")
			}
		} else {
			if evt.Event == "timeout" {
				// Dispatch timeout event before returning error
				source.dispatchQRTimeoutEvent()
				return errors.New("timeout")
			}
			wg.Done()
			break
		}
	}

	wg.Wait()
	return nil
}

func (source *WhatsmeowConnection) HistorySync(timestamp time.Time) (err error) {
	logentry := source.GetLogger()
	return nil
}
