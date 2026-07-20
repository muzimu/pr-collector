package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const htmxRequestHeader = "HX-Request"

// isHTMXRequest reports whether htmx initiated the request. htmx sets this
// header automatically for boosted and attribute-driven requests.
func isHTMXRequest(c *gin.Context) bool {
	return strings.EqualFold(c.GetHeader(htmxRequestHeader), "true")
}

// varyOnHTMXRequest prevents caches from mixing full-page and fragment
// responses served from the same URL.
func varyOnHTMXRequest(c *gin.Context) {
	header := c.Writer.Header()
	for _, value := range header.Values("Vary") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), htmxRequestHeader) {
				return
			}
		}
	}
	header.Add("Vary", htmxRequestHeader)
}

// RenderPageError returns a body fragment to htmx and a complete document to
// normal browser requests.
func RenderPageError(c *gin.Context, status int, message string) {
	varyOnHTMXRequest(c)
	templateName := "error.html"
	if isHTMXRequest(c) {
		templateName = "error_fragment"
	}
	c.HTML(status, templateName, gin.H{"message": message})
}
