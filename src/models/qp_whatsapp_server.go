package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/nocodeleaks/quepasa/library"
	"github.com/nocodeleaks/quepasa/rabbitmq"
	"github.com/nocodeleaks/quepasa/signalr"
	"github.com/nocodeleaks/quepasa/whatsapp"
)

type QpWhatsappServer struct {
	*QpServer
	QpDataDispatching // new dispatching system

	// should auto reconnect, false for qrcode scanner
	Reconnect bool `json:"reconnect"`

	connection     whatsapp.IWhatsappConnection `json:"-"`
	syncConnection *sync.Mutex                  `json:"-"` // Objeto de sinaleiro para evitar chamadas simultâneas a este objeto
	syncMessages   *sync.Mutex                  `json:"-"` // Objeto de sinaleiro para evitar chamadas simultâneas a este objeto

	Timestamps QpTimestamps `json:"timestamps"`

	Handler            *DispatchingHandler        `json:"-"`
	DispatchingHandler *QPDispatchingHandler      `json:"-"`
	GroupManager       *QpGroupManager            `json:"-"` // composition for group operations
	StatusManager      *QpStatusManager           `json:"-"` // composition for status operations
	ContactManager     *QpContactManager          `json:"-"` // composition for contact operations

	// Stop request token
	StopRequested bool
	db            QpDataServersInterface `json:"-"`
}

// MarshalJSON customizes JSON serialization to include only dispatching field instead of webhooks
func (source QpWhatsappServer) MarshalJSON() ([]byte, error) {
	// Create a custom struct to control serialization
	type CustomServer struct {
		*QpServer
		Reconnect   bool             `json:"reconnect"`
		StartTime   time.Time        `json:"starttime,omitempty"`
		Timestamps  QpTimestamps     `json:"timestamps"`
		Dispatching []*QpDispatching `json:"dispatching,omitempty"`
		Uptime      library.Duration `json:"uptime"`
	}

	// Get dispatching data from memory (includes real-time failure/success updates)
	var dispatchingData []*QpDispatching
	if source.QpDataDispatching.Dispatching != nil {
		// Use in-memory dispatching data with real-time status
		dispatchingData = source.QpDataDispatching.Dispatching
	}

	// Prepare timestamps for serialization
	timestamps := source.Timestamps
	timestamps.Update = source.Timestamp

	// Calculate uptime
	uptime := time.Duration(0)
	if !timestamps.Start.IsZero() {
		uptime = time.Since(timestamps.Start)
	}

	return json.Marshal(&CustomServer{
		QpServer:    source.QpServer,
		Reconnect:   source.Reconnect,
		StartTime:   timestamps.Start,
		Timestamps:  timestamps,
		Dispatching: dispatchingData,
		Uptime:      library.Duration(uptime),
	})
}

// region IMPLEMENTING IQPWHATSAPPSERVER INTERFACE

func (source *QpWhatsappServer) GetWid() string {
	if source.connection == nil {
		return ""
	}
	return source.connection.GetWid()
}

func (source *QpWhatsappServer) Download(id string) ([]byte, error) {
	if source.connection == nil {
		return nil, fmt.Errorf("whatsapp connection is nil")
	}
	return source.connection.Download(id)
}

func (source *QpWhatsappServer) SendList(chatId string, list *whatsapp.WhatsappList, trackId string) (IQpMessage, error) {
	if source.connection == nil {
		return nil, fmt.Errorf("whatsapp connection is nil")
	}

	// Convert to whatsmeow proto and send
	msgProto := list.ToProto()
	msg, err := source.connection.SendMessage(chatId, msgProto, trackId)
	if err != nil {
		return nil, err
	}

	return ToQpMessageV2(msg, source), nil
}

// endregion

// region IMPLEMENTING WHATSAPP OPTIONS INTERFACE

func (source *QpWhatsappServer) GetOptions() *whatsapp.WhatsappConnectionOptions {
	if source == nil {
		return nil
	}

	options := &whatsapp.WhatsappConnectionOptions{
		Wid:       source.Wid,
		Reconnect: source.Reconnect,
	}

	return options
}

// endregion

// region IMPLEMENTING WHATSAPP HANDLERS INTERFACE

func (source *QpWhatsappServer) OnMessage(message *whatsapp.WhatsappMessage) {
	if source.Handler != nil {
		source.Handler.OnMessage(ToQpMessageV2(message, source))
	}
}

func (source *QpWhatsappServer) OnMessageStatus(status *whatsapp.WhatsappMessageStatus) {
	if source.Handler != nil {
		source.Handler.OnMessageStatus(status)
	}
}

func (source *QpWhatsappServer) OnConnect() {
	source.Timestamps.Connected = time.Now()
	if source.Handler != nil {
		source.Handler.OnConnect()
	}
}

func (source *QpWhatsappServer) OnDisconnect() {
	source.Timestamps.Disconnected = time.Now()
	if source.Handler != nil {
		source.Handler.OnDisconnect()
	}
}

func (source *QpWhatsappServer) OnQR(qr string) {
	if source.Handler != nil {
		source.Handler.OnQR(qr)
	}
}

// endregion

func (source *QpWhatsappServer) Connect() (err error) {
	source.syncConnection.Lock()
	defer source.syncConnection.Unlock()

	if source.connection != nil {
		if source.connection.IsConnected() {
			return
		}
	}

	// Create connection
	source.connection, err = NewWhatsmeowConnection(*source.GetOptions())
	if err != nil {
		return
	}

	// Set handlers
	source.connection.SetHandler(source)

	// Connect
	err = source.connection.Connect()
	if err == nil {
		source.Timestamps.Start = time.Now()
	}

	return
}

func (source *QpWhatsappServer) Disconnect() (err error) {
	source.syncConnection.Lock()
	defer source.syncConnection.Unlock()

	if source.connection == nil {
		return
	}

	err = source.connection.Disconnect()
	source.connection = nil

	return
}

func (source *QpWhatsappServer) IsConnected() bool {
	if source.connection == nil {
		return false
	}
	return source.connection.IsConnected()
}

func (source *QpWhatsappServer) GetMessages(searchTime time.Time) (messages []*whatsapp.WhatsappMessage) {
	if source.connection == nil {
		return
	}
	return source.connection.GetMessages(searchTime)
}

func (source *QpWhatsappServer) SendMessage(chatId string, text string, trackId string) (message IQpMessage, err error) {
	if source.connection == nil {
		err = fmt.Errorf("whatsapp connection is nil")
		return
	}

	msg, err := source.connection.SendText(chatId, text, trackId)
	if err != nil {
		return
	}

	message = ToQpMessageV2(msg, source)
	return
}

func (source *QpWhatsappServer) SendPoll(chatId string, poll *whatsapp.WhatsappPoll, trackId string) (message IQpMessage, err error) {
	if source.connection == nil {
		err = fmt.Errorf("whatsapp connection is nil")
		return
	}

	msg, err := source.connection.SendPoll(chatId, poll, trackId)
	if err != nil {
		return
	}

	message = ToQpMessageV2(msg, source)
	return
}

func (source *QpWhatsappServer) SendLocation(chatId string, location *whatsapp.WhatsappLocation, trackId string) (message IQpMessage, err error) {
	if source.connection == nil {
		err = fmt.Errorf("whatsapp connection is nil")
		return
	}

	msg, err := source.connection.SendLocation(chatId, location, trackId)
	if err != nil {
		return
	}

	message = ToQpMessageV2(msg, source)
	return
}

func (source *QpWhatsappServer) SendContact(chatId string, contact *whatsapp.WhatsappContact, trackId string) (message IQpMessage, err error) {
	if source.connection == nil {
		err = fmt.Errorf("whatsapp connection is nil")
		return
	}

	msg, err := source.connection.SendContact(chatId, contact, trackId)
	if err != nil {
		return
	}

	message = ToQpMessageV2(msg, source)
	return
}

func (source *QpWhatsappServer) SendAttachment(chatId string, attachment *whatsapp.WhatsappAttachment, trackId string) (message IQpMessage, err error) {
	if source.connection == nil {
		err = fmt.Errorf("whatsapp connection is nil")
		return
	}

	msg, err := source.connection.SendAttachment(chatId, attachment, trackId)
	if err != nil {
		return
	}

	message = ToQpMessageV2(msg, source)
	return
}

func NewQpWhatsappServer(server *QpServer, db QpDataServersInterface) *QpWhatsappServer {
	source := &QpWhatsappServer{
		QpServer:       server,
		syncConnection: &sync.Mutex{},
		syncMessages:   &sync.Mutex{},
		db:             db,
	}

	// Initialize managers
	source.GroupManager = &QpGroupManager{server: source}
	source.StatusManager = &QpStatusManager{server: source}
	source.ContactManager = &QpContactManager{server: source}

	// Initialize dispatching
	source.QpDataDispatching = QpDataDispatching{}

	return source
}
