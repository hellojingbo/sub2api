package service

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) attachGatewayRequestAudit(
	req *http.Request,
	c *gin.Context,
	account *Account,
	body []byte,
	transport string,
) *http.Request {
	if s == nil || s.gatewayRequestAudit == nil || req == nil {
		return req
	}
	draft := GatewayRequestAuditDraft{
		Transport:         transport,
		DownstreamMethod:  http.MethodPost,
		DownstreamHeaders: map[string][]string{},
		DownstreamBody:    append([]byte(nil), body...),
	}
	if c != nil {
		if c.Request != nil {
			draft.DownstreamMethod = c.Request.Method
			draft.DownstreamURL = c.Request.URL.RequestURI()
			draft.DownstreamHeaders = cloneGatewayAuditHeaders(c.Request.Header)
			draft.RequestID = c.Writer.Header().Get("X-Request-ID")
			if draft.RequestID == "" {
				draft.RequestID = c.Request.Header.Get("X-Request-ID")
			}
		}
	}
	if account != nil {
		draft.AccountID = account.ID
		draft.AccountName = account.Name
	}
	return s.gatewayRequestAudit.AttachHTTP(req, draft)
}

func cloneGatewayAuditHeaders(headers http.Header) map[string][]string {
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
