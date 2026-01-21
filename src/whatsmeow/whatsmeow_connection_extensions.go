package whatsmeow

import (
	"context"
	"fmt"

	whatsapp "github.com/nocodeleaks/quepasa/whatsapp"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func (source *WhatsmeowConnection) SendMessage(msg *whatsapp.WhatsappMessage) (whatsapp.IWhatsappSendResponse, error) {
	logentry := source.GetLogger().WithField(LogFields.MessageId, msg.Id)

	// Interactive List Message
	if msg.List != nil {
		logentry.Debug("sending list message")
		return source.SendListMessage(msg)
	}

	// Poll Message
	if msg.Poll != nil {
		logentry.Debug("sending poll message")
		return source.SendPollMessage(msg)
	}

	// Default Send
	return source.Send(msg)
}

func (source *WhatsmeowConnection) SendListMessage(msg *whatsapp.WhatsappMessage) (whatsapp.IWhatsappSendResponse, error) {
	logentry := source.GetLogger().WithField(LogFields.MessageId, msg.Id)

	jid, err := ParseJID(msg.GetChatId())
	if err != nil {
		return nil, err
	}

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

	listMsg := &waE2E.ListMessage{
		Title:       proto.String(msg.List.Title),
		Description: proto.String(msg.List.Description),
		ButtonText:  proto.String(msg.List.ButtonText),
		FooterText:  proto.String(msg.List.FooterText),
		ListType:    waE2E.ListMessage_SINGLE_SELECT.Enum(),
		Sections:    sections,
		ContextInfo: source.GetContextInfo(*msg),
	}

	waMsg := &waE2E.Message{
		ListMessage: listMsg,
	}

	resp, err := source.Client.SendMessage(context.Background(), jid, waMsg)
	if err != nil {
		logentry.Errorf("error sending list message: %v", err)
		return nil, err
	}

	msg.Timestamp = resp.Timestamp
	msg.Id = resp.ID

	return msg, nil
}
