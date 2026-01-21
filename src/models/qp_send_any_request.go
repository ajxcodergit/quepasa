package models

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/nocodeleaks/quepasa/whatsapp"
)

/*
<summary>

	Request to send any type of message
	1º Attachment Url
	2º Attachment Content Base64
	3º Text Message

</summary>
*/
type QpSendAnyRequest struct {
	QpSendRequest

	// Url for download content
	Url string `json:"url,omitempty"`

	// BASE64 embed content
	Content string `json:"content,omitempty"`
}

// From BASE64 content
func (source *QpSendAnyRequest) GenerateEmbedContent() (err error) {
	content := source.Content

	// Check if content is a data URI (e.g., "data:image/png;base64,<base64data>")
	if strings.HasPrefix(content, "data:") {
		// Parse data URI
		parts := strings.SplitN(content, ",", 2)
		if len(parts) != 2 {
			err = fmt.Errorf("invalid data URI format")
			return
		}

		// Extract MIME type from data URI
		header := parts[0]
		if strings.HasPrefix(header, "data:") && strings.Contains(header, ";base64") {
			mimePart := header[5:]                                 // Remove "data:"
			mimeType := strings.Split(mimePart, ";")[0]            // Get MIME before ";base64"
			if len(source.Minetype) == 0 {
				source.Minetype = mimeType
			}
		}

		// Use the base64 part
		content = parts[1]
	}

	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return
	}

	source.QpSendRequest.Content = decoded

	// Set the correct file length for decoded content
	source.FileLength = uint64(len(decoded))

	// If filename is not set, try to generate one
	if len(source.FileName) == 0 {
		source.FileName = "file"
		if len(source.Minetype) > 0 {
			exts := strings.Split(source.Minetype, "/")
			if len(exts) == 2 {
				source.FileName = fmt.Sprintf("file.%s", exts[1])
			}
		}
	}

	return
}

// From URL content
func (source *QpSendAnyRequest) GenerateUrlContent() (err error) {
	// Download content from URL
	response, err := http.Get(source.Url)
	if err != nil {
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("failed to download content from URL: %s", source.Url)
		return
	}

	// Read content
	source.QpSendRequest.Content, err = io.ReadAll(response.Body)
	if err != nil {
		return
	}

	// Set the correct file length
	source.FileLength = uint64(len(source.QpSendRequest.Content))

	// If filename is not set, try to get it from URL
	if len(source.FileName) == 0 {
		u, errUrl := url.Parse(source.Url)
		if errUrl == nil {
			source.FileName = path.Base(u.Path)
		}
	}

	// If minetype is not set, try to get it from response header
	if len(source.Minetype) == 0 {
		source.Minetype = response.Header.Get("Content-Type")
	}

	return
}
