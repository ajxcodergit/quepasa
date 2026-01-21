package whatsapp

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
