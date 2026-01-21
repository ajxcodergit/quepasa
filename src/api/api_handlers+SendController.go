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

// -------------------------- PUBLIC METHODS
//region TYPES OF SENDING

// SendAPIHandler renders route "/send" and "/sendencoded"
func SendAny(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	server, err := GetServer(r)
	if err != nil {
		MessageSendErrors.Inc()
		ObserveAPIRequestDuration(r.Method, "/send", "400", time.Since(startTime).Seconds())

		response := &models.QpSendResponse{}
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	SendAnyWithServer(w, r, server)

	// Record successful API processing time (assuming 200 status for now)
	ObserveAPIRequestDuration(r.Method, "/send", "200", time.Since(startTime).Seconds())
}

func SendAnyWithServer(w http.ResponseWriter, r *http.Request, server *models.QpWhatsappServer) {
	response := &models.QpSendResponse{}

	// Declare a new request struct.
	request := &models.QpSendAnyRequest{}

	if r.ContentLength > 0 && r.Method == http.MethodPost {
		err := json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			jsonErr := fmt.Errorf("invalid json body: %s", err.Error())
			response.ParseError(jsonErr)
			RespondInterface(w, response)
			return
		}
	}

	// Getting ChatId parameter
	err := request.EnsureValidChatId(r)
	if err != nil {
		MessageSendErrors.Inc()
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	if len(request.Url) == 0 && r.URL.Query().Has("url") {
		request.Url = r.URL.Query().Get("url")
	}

	request.Url = strings.TrimSpace(request.Url)

	if len(request.Url) > 0 {
		err = request.GenerateUrlContent()
		if err != nil {
			MessageSendErrors.Inc()
			response.ParseError(err)
			RespondInterface(w, response)
			return
		}
	} else if len(request.Content) > 0 {
		err = request.GenerateEmbedContent()
		if err != nil {
			MessageSendErrors.Inc()
			response.ParseError(err)
			RespondInterface(w, response)
			return
		}
	}

	filename := library.GetFileName(r)
	if len(filename) > 0 {
		request.FileName = filename
	}

	SendRequest(w, r, &request.QpSendRequest, server)
}

//endregion

// -------------------------- INTERNAL METHODS

// Send a request already validated with chatid and server
func SendRequest(w http.ResponseWriter, r *http.Request, request *models.QpSendRequest, server *models.QpWhatsappServer) {
	response := &models.QpSendResponse{}
	var err error

	att := request.ToWhatsappAttachment()

	if len(request.Text) == 0 {
		request.Text = GetTextParameter(r)
	}

	if len(request.InReply) == 0 {
		request.InReply = GetInReplyParameter(r)
	}

	// MODIFIED: Check for list prefix in text to bypass "text not found"
	isList := strings.HasPrefix(strings.TrimSpace(request.Text), "list:")

	if !isList && request.Poll == nil && request.Location == nil && request.Contact == nil && att.Attach == nil && len(request.Text) == 0 {
		MessageSendErrors.Inc()
		err = fmt.Errorf("text not found, do not send empty messages")
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	if len(request.TrackId) == 0 {
		request.TrackId = GetTrackId(r)
	}

	response.Debug = append(response.Debug, att.Debug...)
	Send(server, response, request, w, att.Attach)
}

func Send(server *models.QpWhatsappServer, response *models.QpSendResponse, request *models.QpSendRequest, w http.ResponseWriter, attach *whatsapp.WhatsappAttachment) {
	SendWithMessageType(server, response, request, w, attach, whatsapp.UnhandledMessageType)
}

func SendWithMessageType(server *models.QpWhatsappServer, response *models.QpSendResponse, request *models.QpSendRequest, w http.ResponseWriter, attach *whatsapp.WhatsappAttachment, messageType whatsapp.WhatsappMessageType) {
	waMsg, err := request.ToWhatsappMessage()

	if err != nil {
		MessageSendErrors.Inc()
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	logentry := server.GetLogger()

	pollText := strings.TrimSpace(waMsg.Text)
	if len(pollText) > 0 {
		if strings.HasPrefix(pollText, "poll:") {
			pollText = pollText[5:]
			var poll *whatsapp.WhatsappPoll
			err = json.Unmarshal([]byte(pollText), &poll)
			if err != nil {
				err = fmt.Errorf("error converting text to json poll: %s", err.Error())
				MessageSendErrors.Inc()
				response.ParseError(err)
				RespondInterface(w, response)
				return
			}
			waMsg.Poll = poll
		} else if strings.HasPrefix(pollText, "list:") {
			// MODIFIED: Support list via text prefix
			listJson := pollText[5:]
			var list *whatsapp.WhatsappList
			err = json.Unmarshal([]byte(listJson), &list)
			if err != nil {
				err = fmt.Errorf("error converting text to json list: %s", err.Error())
				MessageSendErrors.Inc()
				response.ParseError(err)
				RespondInterface(w, response)
				return
			}
			waMsg.List = list
		}
	}

	if attach != nil {
		waMsg.Attachment = attach
		if messageType == whatsapp.UnhandledMessageType {
			waMsg.Type = whatsapp.GetMessageType(attach)
		} else {
			waMsg.Type = messageType
		}
	} else {
		if waMsg.Type == whatsapp.UnhandledMessageType {
			waMsg.Type = whatsapp.TextMessageType
		}
	}

	if waMsg.List != nil {
		// List messages are handled in whatsmeow_connection.go
	} else if waMsg.Type == whatsapp.UnhandledMessageType {
		if len(waMsg.Text) > 0 {
			waMsg.Type = whatsapp.TextMessageType
		} else {
			err = fmt.Errorf("unknown message type without text")
			response.ParseError(err)
			RespondInterface(w, response)
			return
		}
	}

	status := server.GetStatus()
	if status != whatsapp.Ready {
		err = fmt.Errorf("whatsapp server is not ready, current status: %v", status)
		MessageSendErrors.Inc()
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	conn, err := server.GetValidConnection()
	if err != nil {
		MessageSendErrors.Inc()
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	res, err := conn.Send(waMsg)
	if err != nil {
		MessageSendErrors.Inc()
		response.ParseError(err)
		RespondInterface(w, response)
		return
	}

	response.Success = true
	response.Status = "sent"
	response.Id = res.GetId()
	response.Timestamp = res.GetTime()

	RespondInterface(w, response)
}
