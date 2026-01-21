package models

import (
	"context"
	"fmt"

	whatsapp "github.com/nocodeleaks/quepasa/whatsapp"
	log "github.com/sirupsen/logrus"
)

// get default log entry, never nil
func (source *QpWhatsappServer) GetLogger() *log.Entry {
	logentry := log.WithContext(context.Background())
	logentry = logentry.WithField(LogFields.WId, source.Wid)

	return logentry
}

func (source *QpWhatsappServer) GetStatus() whatsapp.WhatsappConnectionState {
	if source.connection == nil {
		return whatsapp.Unlogged
	}
	return source.connection.GetResume().State
}

func (source *QpWhatsappServer) GetNumber() string {
	return source.Wid
}

// MODIFIED: Added SendMessage to handle the new logic in whatsmeow_connection_extensions.go
func (source *QpWhatsappServer) SendMessage(msg *whatsapp.WhatsappMessage) (whatsapp.IWhatsappSendResponse, error) {
	if source.connection == nil {
		return nil, fmt.Errorf("whatsapp connection not found")
	}

	// We need to cast the connection to access the new SendMessage method
	type ISendMessage interface {
		SendMessage(*whatsapp.WhatsappMessage) (whatsapp.IWhatsappSendResponse, error)
	}

	if conn, ok := source.connection.(ISendMessage); ok {
		return conn.SendMessage(msg)
	}

	return source.connection.Send(msg)
}
