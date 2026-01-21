package whatsapp

import (
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type WhatsappList struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	ButtonText  string        `json:"buttonText"`
	FooterText  string        `json:"footerText"`
	Sections    []ListSection `json:"sections"`
}

type ListSection struct {
	Title string    `json:"title"`
	Rows  []ListRow `json:"rows"`
}

type ListRow struct {
	RowId       string `json:"rowId"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ToProto converts WhatsappList to waE2E.Message
func (l *WhatsappList) ToProto() *waE2E.Message {
	sections := make([]*waE2E.ListMessage_Section, len(l.Sections))
	for i, s := range l.Sections {
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

	return &waE2E.Message{
		ListMessage: &waE2E.ListMessage{
			Title:       proto.String(l.Title),
			Description: proto.String(l.Description),
			ButtonText:  proto.String(l.ButtonText),
			FooterText:  proto.String(l.FooterText),
			ListType:    waE2E.ListMessage_SINGLE_SELECT.Enum(),
			Sections:    sections,
		},
	}
}
