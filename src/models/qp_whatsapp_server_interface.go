package models

import "github.com/nocodeleaks/quepasa/whatsapp"

type IQpWhatsappServer interface {
	// Returns whatsapp controller id on E164
	GetWid() string

	// Download message attachments
	Download(id string) ([]byte, error)

	// Send list message
	SendList(chatId string, list *whatsapp.WhatsappList, trackId string) (IQpMessage, error)
}
