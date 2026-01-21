package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	library "github.com/nocodeleaks/quepasa/library"
	models "github.com/nocodeleaks/quepasa/models"
	whatsapp "github.com/nocodeleaks/quepasa/whatsapp"
)

// ------------------------------ PUBLIC METHODS
//region TYPES OF SENDING

// SendAPIHandler renders route "/send" and "/sendencoded"
//
//	@Summary		Send any type of message (text, file, poll, base64 content, location, contact, list)
//	@Description	Endpoint to send messages via WhatsApp. Accepts sending of:
//	@Description	- Plain text (field "text")
//	@Description	- Files by URL (field "url") - server will download and send as attachment
//	@Description	- Base64 content (field "content") - use format data:<mime>;base64,<data>
//	@Description	- Polls (field "poll") - send the poll JSON in the "poll" field
//	@Description	- Location (field "location") - send location with latitude/longitude in the "location" object
//	@Description	- Contact (field "contact") - send contact with phone/name in the "contact" object
//	@Description	- List (field "list") - send interactive list message in the "list" object
//
//	@Description	Main fields:
//	@Description	- chatId: chat identifier (can be WID, LID or number with suffix @s.whatsapp.net)
//	@Description	- text: message text
//	@Description	- trackId: optional ID for tracking the message
//	@Description	- url: public URL to download a file
//	@Description	- content: embedded base64 content (e.g.: data:image/png;base64,...)
//	@Description	- fileName: file name (optional, used when name cannot be inferred)
//	@Description	- poll: JSON object with the poll (question, options, selections)
//	@Description	- location: JSON object with location data (latitude, longitude, name, address, url)
//	@Description	- contact: JSON object with contact data (phone, name, vcard)
//	@Description	- list: JSON object with list data (title, description, buttonText, footerText, sections)
//
//	@Tags			Sending
//	@Accept			json
//	@Produce		json
//	@Param			request	body		models.QpSendAnyRequest	true	"Message details"
//	@Success		200		{object}	models.QpMessageV2
//	@Failure		400		{object}	library.ResponseError
//	@Failure		401		{object}	library.ResponseError
//	@Failure		500		{object}	library.ResponseError
//	@Router			/send [post]
func SendAPIHandler(w http.ResponseWriter, r *http.Request) {
	SendAnyWithServer(w, r)
}

// SendAnyWithServer handles the generic sending logic
func SendAnyWithServer(w http.ResponseWriter, r *http.Request) {
	var request models.QpSendAnyRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		library.WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Get server from context (assuming it's injected by middleware)
	server, ok := r.Context().Value("server").(models.IQpWhatsappServer)
	if !ok {
		library.WriteError(w, http.StatusInternalServerError, "Server not found in context")
		return
	}

	var message models.IQpMessage

	// Priority 1: List Message
	if request.List != nil {
		message, err = server.SendList(request.ChatId, request.List, request.TrackId)
	} else if request.Poll != nil {
		// Priority 2: Poll
		message, err = server.SendPoll(request.ChatId, request.Poll, request.TrackId)
	} else if request.Location != nil {
		// Priority 3: Location
		message, err = server.SendLocation(request.ChatId, request.Location, request.TrackId)
	} else if request.Contact != nil {
		// Priority 4: Contact
		message, err = server.SendContact(request.ChatId, request.Contact, request.TrackId)
	} else if len(request.Url) > 0 {
		// Priority 5: URL Attachment
		err = request.GenerateUrlContent()
		if err == nil {
			attachment := &whatsapp.WhatsappAttachment{
				Content:  request.Content,
				FileName: request.FileName,
				Minetype: request.Minetype,
			}
			message, err = server.SendAttachment(request.ChatId, attachment, request.TrackId)
		}
	} else if len(request.Content) > 0 {
		// Priority 6: Base64 Attachment
		err = request.GenerateEmbedContent()
		if err == nil {
			attachment := &whatsapp.WhatsappAttachment{
				Content:  request.Content,
				FileName: request.FileName,
				Minetype: request.Minetype,
			}
			message, err = server.SendAttachment(request.ChatId, attachment, request.TrackId)
		}
	} else if len(request.Text) > 0 {
		// Priority 7: Plain Text
		message, err = server.SendMessage(request.ChatId, request.Text, request.TrackId)
	} else {
		err = fmt.Errorf("no message content provided (text, url, content, poll, location, contact or list)")
	}

	if err != nil {
		library.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	library.WriteJSON(w, http.StatusOK, message)
}

//endregion
